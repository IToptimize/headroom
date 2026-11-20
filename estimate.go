//go:build windows

package main

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// ---- small settings store in HKCU\Software\Headroom ----

func regGet(name string) string {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(settingsKey))), 0, KEY_READ, ptr(&k)); r != 0 {
		return ""
	}
	defer pRegCloseKey.Call(k)
	buf := make([]uint16, 128)
	n := uint32(len(buf) * 2)
	var typ uint32
	if r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(u16(name))), 0, ptr(&typ), uintptr(unsafe.Pointer(&buf[0])), ptr(&n)); r != 0 || typ != REG_SZ {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func regSet(name, val string) {
	var k uintptr
	if r, _, _ := pRegCreateKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(settingsKey))), 0, 0, 0, KEY_ALL_ACCESS, 0, ptr(&k), 0); r != 0 {
		return
	}
	defer pRegCloseKey.Call(k)
	v := syscall.StringToUTF16(val)
	pRegSetValueExW.Call(k, uintptr(unsafe.Pointer(u16(name))), 0, REG_SZ, uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2))
}

func regFloat(name string, def float64) float64 {
	if f, err := strconv.ParseFloat(regGet(name), 64); err == nil {
		return f
	}
	return def
}

func lightTheme() bool {
	var k uintptr
	key := `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(key))), 0, KEY_READ, ptr(&k)); r != 0 {
		return false
	}
	defer pRegCloseKey.Call(k)
	var v, n uint32 = 0, 4
	if r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(u16("SystemUsesLightTheme"))), 0, 0, ptr(&v), ptr(&n)); r != 0 {
		return false
	}
	return v == 1
}

// ---- battery estimation ----

const (
	defaultChargeHrs = 3.5
	gapReset         = 5 * time.Minute
	minSpan          = 20 * time.Minute
	minDrop          = 3.0
	sampleWindow     = 4 * time.Hour
)

type sample struct {
	t time.Time
	l float64
}

// View is everything the UI shows.
type View struct {
	Detail     string // per-part levels, e.g. AirPods L/R/case
	Parts      []Part
	ID         string
	Kind       string
	Tested     bool
	OK         bool
	Name       string
	Err        string
	Level      int  // -1 unknown
	Estimated  bool // level is an estimate (charging on a voltage-only headset)
	Charging   bool
	Full       bool
	Left       time.Duration // time to empty, or to full when charging; 0 = unknown
	LeftRated  bool          // based on rated battery life, not measured
	Estimating bool          // no drain data yet
	Updated    time.Time
}

type tracker struct {
	id          string
	lastSeen    time.Time
	med         []sample
	samples     []sample
	charging    bool
	chgStart    time.Time
	chgStartLvl int
	learn       *struct {
		elapsed  time.Duration
		startLvl int
		n        int
	}
	drainRate  float64   // %/hour, 0 = unknown
	coarse     bool      // device reports in big steps (Nova Pro 12.5 %, AirPods 10 %)
	prevLevel  int       // last discharging level, -1 none
	stepT      time.Time // when the last step drop was seen
	stepL      float64   // level after that drop
	partTr     map[string]*partTrack
	chargeHrs  float64
	lastLevel  int
	savedLevel int
	savedRate  float64
	v          View
}

// key namespaces a setting per device; the Cloud Flight values saved by the
// single-device version (no prefix) are picked up as a fallback.
func (t *tracker) key(name string) string { return t.id + "|" + name }

func (t *tracker) get(name string, def float64) float64 {
	if v := regFloat(t.key(name), -999); v != -999 {
		return v
	}
	if strings.HasPrefix(t.id, "hid:0951:1723") {
		return regFloat(name, def)
	}
	return def
}

func (t *tracker) set(name, val string) { regSet(t.key(name), val) }

func newTracker(id string) *tracker {
	t := &tracker{id: id, lastLevel: -1, savedLevel: -1, prevLevel: -1}
	t.coarse = t.get("Coarse", 0) == 1
	t.drainRate = t.get("DrainRate", 0)
	t.savedRate = t.drainRate
	t.chargeHrs = t.get("ChargeHours", defaultChargeHrs)
	if ts := t.get("LastTime", 0); ts > 0 && time.Since(time.Unix(int64(ts), 0)) < 12*time.Hour {
		t.lastLevel = int(t.get("LastLevel", -1))
	}
	return t
}

func median(v []sample) int {
	s := make([]float64, len(v))
	for i, p := range v {
		s[i] = p.l
	}
	sort.Float64s(s)
	return int(s[len(s)/2])
}

// stepTiming handles devices that report in big steps: a slope across a
// single step is meaningless (the session may have started just before the
// step), so the rate comes from the time between two consecutive step drops.
// A big jump up without charging means a swapped/recharged battery.
func (t *tracker) stepTiming(level int, now time.Time) {
	prev := t.prevLevel
	t.prevLevel = level
	if prev < 0 {
		return
	}
	switch {
	case level > prev+8: // battery swapped (Nova Pro) or charged elsewhere
		dbg("%s: level jumped %d -> %d, starting over", t.id, prev, level)
		t.samples, t.stepT = nil, time.Time{}
	case level < prev:
		if prev-level >= 5 && !t.coarse {
			t.coarse = true
			t.set("Coarse", "1")
			t.samples = nil
		}
		if !t.coarse {
			return
		}
		if !t.stepT.IsZero() {
			if h := now.Sub(t.stepT).Hours(); h > 0.25 {
				r := (t.stepL - float64(level)) / h
				if r > 0.3 && r < 60 {
					if t.drainRate > 0 {
						r = 0.5*t.drainRate + 0.5*r // blend with what we knew
					}
					dbg("%s: step %v -> %d took %.2f h, rate %.2f %%/h", t.id, t.stepL, level, h, r)
					t.drainRate = r
				}
			}
		}
		t.stepT, t.stepL = now, float64(level)
	}
}

// slope returns %/hour drop via least squares.
func slope(s []sample) float64 {
	t0 := s[0].t
	var sx, sy, sxx, sxy float64
	for _, p := range s {
		x := p.t.Sub(t0).Hours()
		sx += x
		sy += p.l
		sxx += x * x
		sxy += x * p.l
	}
	n := float64(len(s))
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return -(n*sxy - sx*sy) / den
}

func (t *tracker) update(r Reading, now time.Time) View {
	v := View{Detail: r.Detail, Parts: r.Parts, ID: r.ID, Kind: r.Kind, Tested: r.Tested, OK: r.OK, Name: r.Name, Err: r.Err, Level: -1, Updated: now}
	if v.Name == "" {
		v.Name = "HyperX headset"
	}
	if !r.OK {
		t.v = v
		return v
	}
	if !t.lastSeen.IsZero() && now.Sub(t.lastSeen) > gapReset {
		t.med, t.samples = nil, nil
		t.stepT, t.prevLevel = time.Time{}, -1
	}
	t.lastSeen = now

	charging := r.Charging == 1
	if charging && !t.charging {
		t.chgStart, t.chgStartLvl = now, t.lastLevel
		t.med, t.samples, t.learn = nil, nil, nil
	}
	if !charging && t.charging {
		t.med, t.samples = nil, nil
		if r.Level >= 0 && t.chgStartLvl >= 0 && now.Sub(t.chgStart) > minSpan {
			t.learn = &struct {
				elapsed  time.Duration
				startLvl int
				n        int
			}{now.Sub(t.chgStart), t.chgStartLvl, 0}
		}
	}
	t.charging = charging
	v.Charging = charging

	switch {
	case r.Charging == 2:
		v.Full, v.Level = true, 100
	case charging:
		elapsed := now.Sub(t.chgStart).Hours()
		if r.Level >= 0 {
			v.Level = r.Level
		} else if t.chgStartLvl >= 0 {
			v.Level = min(99, t.chgStartLvl+int(elapsed/t.chargeHrs*100))
			v.Estimated = true
		}
		if v.Level >= 0 {
			v.Left = time.Duration(float64(100-v.Level) / 100 * t.chargeHrs * float64(time.Hour))
			v.LeftRated = true
		}
	default:
		// smooth voltage noise: median over the last 5 minutes of readings.
		// Step-reporting devices are exact, and a big jump (battery swap)
		// must show at once, so those skip/clear the window.
		if len(t.med) > 0 && math.Abs(float64(r.Level-median(t.med))) >= 8 {
			t.med = nil
		}
		t.med = append(t.med, sample{now, float64(r.Level)})
		for len(t.med) > 1 && now.Sub(t.med[0].t) > smoothWindow {
			t.med = t.med[1:]
		}
		v.Level = median(t.med)
		if t.coarse {
			v.Level = r.Level
		}

		if t.learn != nil {
			if t.learn.n++; t.learn.n >= 3 {
				gain := float64(v.Level - t.learn.startLvl)
				if v.Level < 95 && gain >= 10 {
					t.chargeHrs = math.Max(1.5, math.Min(6, t.learn.elapsed.Hours()*100/gain))
					t.set("ChargeHours", strconv.FormatFloat(t.chargeHrs, 'f', 2, 64))
				}
				t.learn = nil
			}
		}

		t.stepTiming(v.Level, now)

		t.samples = append(t.samples, sample{now, float64(v.Level)})
		for len(t.samples) > 2 && now.Sub(t.samples[0].t) > sampleWindow {
			t.samples = t.samples[1:]
		}
		if n := len(t.samples); n >= 3 && !t.coarse {
			span := t.samples[n-1].t.Sub(t.samples[0].t)
			drop := t.samples[0].l - t.samples[n-1].l
			if span >= minSpan && drop >= minDrop {
				if s := slope(t.samples); s > 0.5 && s < 50 {
					t.drainRate = s
				}
			}
		}
		if t.drainRate > 0 {
			v.Left = time.Duration(float64(v.Level) / t.drainRate * float64(time.Hour))
		} else if rh := ratedHoursFor(v.Name); rh > 0 {
			v.Left = time.Duration(float64(v.Level) / 100 * rh * float64(time.Hour))
			v.LeftRated = true
		} else {
			v.Estimating = true
		}
		if math.Abs(t.drainRate-t.savedRate) > 0.05 {
			t.set("DrainRate", strconv.FormatFloat(t.drainRate, 'f', 3, 64))
			t.savedRate = t.drainRate
		}
	}
	if v.Level >= 0 {
		t.lastLevel = v.Level
		if v.Level != t.savedLevel {
			t.set("LastLevel", strconv.Itoa(v.Level))
			t.set("LastTime", strconv.FormatInt(now.Unix(), 10))
			t.savedLevel = v.Level
		}
	}
	t.annotateParts(&v)
	t.v = v
	return v
}

// partTrack learns how fast a charging part (e.g. the Nova Pro spare in the
// base) fills, from the time between its level steps.
type partTrack struct {
	lastL int
	stepT time.Time
	rate  float64 // %/hour
}

const defaultPartChargeHrs = 3.0

// annotateParts puts the time text under each part's %: the device estimate
// under the battery in use, a time-to-full under a charging part.
func (t *tracker) annotateParts(v *View) {
	if len(v.Parts) == 0 {
		return
	}
	parts := append([]Part(nil), v.Parts...)
	v.Parts = parts
	if t.partTr == nil {
		t.partTr = map[string]*partTrack{}
	}
	main := -1
	for i, p := range parts {
		if p.Label == "Case" || strings.HasPrefix(p.Label, "Spare") || p.Level < 0 {
			continue
		}
		if main < 0 || p.Level < parts[main].Level {
			main = i
		}
	}
	now := v.Updated
	for i := range parts {
		p := &parts[i]
		if i == main {
			if lt := leftText(*v); lt != "" {
				p.Sub = strings.TrimSuffix(lt, " (rated)")
				if v.LeftRated {
					p.Sub += " (rated)"
				}
			}
			continue
		}
		if !p.Charging || p.Level < 0 || p.Level >= 100 || i == main {
			delete(t.partTr, p.Label)
			continue
		}
		pt := t.partTr[p.Label]
		if pt == nil {
			pt = &partTrack{lastL: p.Level, rate: t.get("part|"+p.Label+"|Rate", 0)}
			t.partTr[p.Label] = pt
		}
		if p.Level > pt.lastL {
			if !pt.stepT.IsZero() {
				if h := now.Sub(pt.stepT).Hours(); h > 0.05 {
					r := float64(p.Level-pt.lastL) / h
					if r > 5 && r < 200 {
						if pt.rate > 0 {
							r = 0.5*pt.rate + 0.5*r
						}
						pt.rate = r
						t.set("part|"+p.Label+"|Rate", strconv.FormatFloat(r, 'f', 2, 64))
					}
				}
			}
			pt.stepT, pt.lastL = now, p.Level
		}
		rate := pt.rate
		if rate <= 0 {
			rate = 100 / defaultPartChargeHrs
		}
		p.Sub = "≈ " + fmtDur(time.Duration(float64(100-p.Level)/rate*float64(time.Hour))) + " to full"
	}
}

func fmtDur(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m < 1:
		return "< 1 min"
	case m < 60:
		return strconv.Itoa(m) + " min"
	case m < 600:
		if m%60 == 0 {
			return strconv.Itoa(m/60) + " h"
		}
		return strconv.Itoa(m/60) + " h " + strconv.Itoa(m%60) + " min"
	}
	return strconv.Itoa((m+30)/60) + " h"
}

const smoothWindow = 5 * time.Minute

// ratedHoursFor: manufacturer battery life, used until a real drain rate is
// measured. Only models with a confirmed figure are listed.
func ratedHoursFor(name string) float64 {
	switch {
	case strings.Contains(name, "Cloud Flight"):
		return 30 // LEDs off
	case strings.Contains(name, "Arctis Nova Pro Wireless"):
		return 20 // SteelSeries: 18-22 h per battery
	case strings.Contains(name, "Arctis Nova 5"):
		return 60
	}
	return 0
}
