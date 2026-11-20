//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type theme struct{ bg, border, fg, muted, track color }

func currentTheme() theme {
	if panelLight() {
		return theme{
			bg: color{249, 249, 249, 0.98}, border: color{0, 0, 0, 0.10},
			fg: color{20, 20, 20, 1}, muted: color{0, 0, 0, 0.58}, track: color{0, 0, 0, 0.09},
		}
	}
	return theme{
		bg: color{36, 36, 38, 0.97}, border: color{255, 255, 255, 0.09},
		fg: color{255, 255, 255, 1}, muted: color{255, 255, 255, 0.62}, track: color{255, 255, 255, 0.12},
	}
}

var (
	colGreen = color{34, 197, 94, 1}
	colAmber = color{245, 158, 11, 1}
	colRed   = color{239, 68, 68, 1}
	colBlue  = color{59, 130, 246, 1}
	colGrey  = color{140, 140, 145, 1}
)

func levelColor(v View) color {
	if !v.OK || v.Level < 0 && !v.Charging {
		return colGrey
	}
	return batteryColor(v.Level, v.Charging || v.Full)
}

// batteryColor applies the colour setting: battery colours, or one accent
// colour that still turns red when low.
func batteryColor(level int, charging bool) color {
	if cfg.ColorMode == 1 {
		if !charging && level >= 0 && level <= 10 {
			return colRed
		}
		return accentColor()
	}
	switch {
	case charging:
		return colBlue
	case level <= 10:
		return colRed
	case level < 30:
		return colAmber
	}
	return colGreen
}

func statusText(v View) string {
	switch {
	case !v.OK:
		return v.Err
	case v.Full:
		return "Fully charged"
	case v.Charging:
		return "Charging"
	}
	return "On battery"
}

func leftText(v View) string {
	if !v.OK {
		return ""
	}
	if v.Estimating {
		return "Estimating time left…"
	}
	if v.Left <= 0 {
		return ""
	}
	if v.Charging {
		return "≈ " + fmtDur(v.Left) + " to full"
	}
	s := "≈ " + fmtDur(v.Left) + " left"
	if v.LeftRated {
		s += " (rated)"
	}
	return s
}

func levelText(v View) string {
	if v.Level < 0 {
		if v.Charging {
			return "+"
		}
		return "–"
	}
	return fmt.Sprint(v.Level)
}

func shortName(n string) string { return strings.TrimPrefix(n, "HyperX ") }

func tooltip(v View) string {
	if !v.OK {
		return shortName(v.Name) + "\n" + v.Err
	}
	l := ""
	if v.Level >= 0 {
		l = levelText(v) + "% · "
		if v.Estimated {
			l = "≈ " + l
		}
	}
	st := statusText(v)
	if v.Detail != "" {
		st = v.Detail
		l = ""
	}
	s := shortName(v.Name) + "\n" + l + st
	if t := leftText(v); t != "" && !v.Estimating {
		s += "\n" + t
	}
	return s
}

// tooltipAll lists every device, one line each (tray tooltips max 127 chars).
func tooltipAll(vs []View) string {
	if len(vs) == 0 {
		return appName + "\nNo headset found"
	}
	if len(vs) == 1 {
		return tooltip(vs[0])
	}
	s := appName
	for _, v := range vs {
		line := shortName(v.Name) + ": "
		switch {
		case !v.OK:
			line += "off"
		case v.Level < 0:
			line += statusText(v)
		default:
			line += levelText(v) + "%"
			if v.Charging {
				line += " ⚡"
			}
		}
		if len(s)+1+len(line) > 120 {
			break
		}
		s += "\n" + line
	}
	return s
}

// ---- tray icon: rounded badge with the number ----

func makeTrayIcon(v View, checking bool) uintptr {
	size, _, _ := pGetSystemMetrics.Call(SM_CXSMICON)
	sz := int(size)
	if sz <= 0 {
		sz = 16
	}
	return drawTrayIcon(v, checking, sz, lightTheme()).icon()
}

// drawTrayIcon draws the tray icon in the chosen style: ring, ring with the
// number inside, or a pie. Filled part = battery left; a centre dot = charging.
func drawTrayIcon(v View, checking bool, sz int, light bool) *canvas {
	c := newCanvas(sz, sz)
	f := float64(sz)
	cx := f / 2
	track := color{255, 255, 255, 0.30}
	fg := color{255, 255, 255, 1}
	if light {
		track, fg = color{0, 0, 0, 0.22}, color{0, 0, 0, 0.85}
	}
	frac := -1.0
	if !checking && v.OK && v.Level >= 0 {
		frac = math.Max(0.04, float64(v.Level)/100)
	}
	col := levelColor(v)
	switch cfg.Icon {
	case 2: // pie
		r := f/2 - 0.5
		c.fillCircle(cx, cx, r, track)
		if frac > 0 {
			c.ring(cx, cx, r/2, r, frac, col) // a ring as thick as the radius = a pie
		}
		if v.Charging && !checking && v.OK {
			c.fillCircle(cx, cx, math.Max(1.6, f*0.12), fg)
		}
	case 1: // ring + number
		t := math.Max(1.6, f*0.10)
		r := f/2 - t/2 - 0.3
		c.ring(cx, cx, r, t, 1, track)
		if frac > 0 {
			c.ring(cx, cx, r, t, frac, col)
			txt := levelText(v)
			px := int(f * 0.62)
			for ; px > 5; px-- {
				m := renderText(txt, px, 700, -1)
				x0, _, x1, _ := m.ink()
				if float64(x1-x0+1) <= f-2*t-2 {
					break
				}
			}
			c.textInkCentered(txt, cx, cx, px, 700, -1, fg)
		}
	default: // ring
		t := math.Max(2.8, f*0.19)
		r := f/2 - t/2 - 0.5
		c.ring(cx, cx, r, t, 1, track)
		if frac > 0 {
			c.ring(cx, cx, r, t, frac, col)
		}
		if v.Charging && !checking && v.OK {
			c.fillCircle(cx, cx, math.Max(1.6, f*0.13), col)
		}
	}
	return c
}

// ---- layered popup windows ----

const (
	WS_POPUP          = 0x80000000
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_LAYERED     = 0x00080000
	WS_EX_NOACTIVATE  = 0x08000000
	WM_ACTIVATE       = 0x0006
	WM_TIMER          = 0x0113
	WM_KEYDOWN        = 0x0100
	SW_HIDE           = 0
	SW_SHOWNOACTIVATE = 4
	SW_SHOW           = 5
)

var (
	flyHwnd, toastHwnd uintptr
	flyVisible         bool
	flyHidden          time.Time
	flyX, flyY         int
	flyScale           float64
	lastFlyH           int
)

func registerPopupClass(name string, proc func(h, msg, wp, lp uintptr) uintptr) {
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512) // IDC_ARROW
	wc := WNDCLASSEXW{LpfnWndProc: syscall.NewCallback(proc), HInstance: hinst, HCursor: cur, LpszClassName: u16(name)}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(ptr(&wc))
}

func createPopup(class string, exStyle uintptr) uintptr {
	hinst, _, _ := pGetModuleHandleW.Call(0)
	h, _, _ := pCreateWindowExW.Call(exStyle|WS_EX_LAYERED|WS_EX_TOOLWINDOW|WS_EX_TOPMOST,
		uintptr(unsafe.Pointer(u16(class))), 0, WS_POPUP, 0, 0, 0, 0, 0, 0, hinst, 0)
	return h
}

func initPopups() {
	registerPopupClass("HXFlyout", flyoutProc)
	registerPopupClass("HXToast", toastProc)
	registerPopupClass("HXSettings", settingsProc)
	setHwnd = createPopup("HXSettings", 0)
	flyHwnd = createPopup("HXFlyout", 0)
	toastHwnd = createPopup("HXToast", WS_EX_NOACTIVATE)
}

func monitorAt(pt POINT, primary bool) (RECT, float64) {
	flags := uintptr(2) // NEAREST
	if primary {
		flags = 1
	}
	mon, _, _ := pMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, flags)
	mi := MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	pGetMonitorInfoW.Call(mon, ptr(&mi))
	scale := 1.0
	if pGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := pGetDpiForMonitor.Call(mon, 0, ptr(&dx), ptr(&dy)); r == 0 && dx > 0 {
			scale = float64(dx) / 96
		}
	}
	return mi.RcWork, scale
}

const shadowPad = 18.0

// card draws the popup background (shadow, rounded body, hairline border) and
// returns the canvas plus the body's top-left in pixels.
func card(w, h, s float64, th theme) (*canvas, float64, float64) {
	pad := shadowPad * s
	c := newCanvas(int(math.Ceil(w+2*pad)), int(math.Ceil(h+2*pad)))
	r := 10 * s
	c.shadow(pad, pad, w, h, r, pad*0.9, 0.30)
	c.fillRoundRect(pad, pad, w, h, r, th.bg)
	c.strokeRoundRect(pad, pad, w, h, r, math.Max(1, s), th.border)
	return c, pad, pad
}

// ---- flyout (click the tray icon) ----

const (
	flyW   = 340.0
	rowH   = 76.0
	footH  = 30.0
	padTop = 8.0
)

// lay holds the row metrics for the chosen density.
type lay struct {
	row, ringR, ringT, nameY, statY, head, part, partR float64
}

func layout() lay {
	if cfg.Compact {
		return lay{row: 56, ringR: 17, ringT: 4, nameY: 8, statY: 27, head: 34, part: 36, partR: 8}
	}
	return lay{row: rowH, ringR: 24, ringT: 5, nameY: 13, statY: 33, head: 42, part: 42, partR: 10}
}

func rowHeight(v View) float64 {
	L := layout()
	if len(v.Parts) > 0 {
		return L.head + float64(len(v.Parts))*L.part + 6
	}
	return L.row
}

// renderFlyout draws one row per device: ring with %, name, status, time left.
// Devices with several batteries (AirPods) get a header plus one line per part.
func renderFlyout(vs []View, s float64) *canvas {
	th := currentTheme()
	L := layout()
	total := 0.0
	for _, v := range vs {
		total += rowHeight(v)
	}
	if len(vs) == 0 {
		total = L.row
	}
	W, H := flyW*s, (padTop+total+footH)*s
	c, ox, oy := card(W, H, s, th)
	if len(vs) == 0 {
		cy := oy + (padTop+rowH/2)*s
		c.ring(ox+44*s, cy, 22*s, 5*s, 1, th.track)
		c.text("No headset found", ox+84*s, cy-20*s, int(14*s), 600, th.fg, alignLeft)
		c.text("Plug in the dongle or connect a Bluetooth headset", ox+84*s, cy+2*s, int(12*s), 400, th.muted, alignLeft)
	}
	y := oy + padTop*s
	for i, v := range vs {
		top := y
		y += rowHeight(v) * s
		if i > 0 {
			c.fillRoundRect(ox+16*s, top, W-32*s, math.Max(1, s), 0, th.border)
		}
		col := levelColor(v)
		tag := v.Kind
		if !v.Tested {
			tag += " · untested"
		}

		if len(v.Parts) > 0 {
			x := ox + 24*s
			nw := c.text(shortName(v.Name), x, top+(L.nameY-1)*s, int(14*s), 600, th.fg, alignLeft)
			c.text(tag, x+float64(nw)+6*s, top+(L.nameY+1)*s, int(11*s), 400, th.muted, alignLeft)
			for j, p := range v.Parts {
				py := top + (L.head+float64(j)*L.part+L.part/2)*s
				pc := partColor(p)
				if p.Note != "" && p.Level >= 0 {
					pc = pc.alpha(0.55) // last known value: dimmed
				}
				cx := ox + 34*s
				c.ring(cx, py, L.partR*s, 3.5*s, 1, th.track)
				if p.Level >= 0 {
					c.ring(cx, py, L.partR*s, 3.5*s, math.Max(0.04, float64(p.Level)/100), pc)
				}
				c.text(p.Label, ox+56*s, py-9*s, int(13*s), 400, th.fg, alignLeft)
				val, vcol := "–", th.muted
				if p.Level >= 0 {
					val, vcol = fmt.Sprintf("%d%%", p.Level), th.fg
				}
				vy := py - 9*s
				if p.Sub != "" {
					vy = py - 15*s // make room for the small line underneath
				}
				vw := c.text(val, ox+W-24*s, vy, int(13*s), 600, vcol, alignRight)
				if p.Sub != "" {
					c.text(p.Sub, ox+W-24*s, py+2*s, int(11*s), 400, th.muted, alignRight)
				}
				note := p.Note
				if p.Level >= 0 && p.Charging {
					note = "Charging"
				}
				if note != "" {
					c.text(note, ox+W-24*s-float64(vw)-10*s, vy+1*s, int(12*s), 400, th.muted, alignRight)
				}
			}
			continue
		}

		cx, cy := ox+44*s, top+L.row/2*s
		c.ring(cx, cy, L.ringR*s, L.ringT*s, 1, th.track)
		if v.OK && v.Level >= 0 {
			c.ring(cx, cy, L.ringR*s, L.ringT*s, math.Max(0.03, float64(v.Level)/100), col)
		}
		label := levelText(v)
		if !v.OK {
			label = "–"
		}
		lp := 15.0
		if cfg.Compact {
			lp = 12
		}
		c.textInkCentered(label, cx, cy, int(lp*s), 700, 0, th.fg)

		x := ox + 86*s
		nw := c.text(shortName(v.Name), x, top+L.nameY*s, int(14*s), 600, th.fg, alignLeft)
		c.text(tag, x+float64(nw)+6*s, top+(L.nameY+2)*s, int(11*s), 400, th.muted, alignLeft)

		dot := 3.5 * s
		c.fillCircle(x+dot, top+(L.statY+9)*s, dot, col)
		st := statusText(v)
		if v.Estimated && v.Level >= 0 {
			st += " · estimated"
		}
		if lt := leftText(v); lt != "" {
			st += "  ·  " + lt
		}
		c.text(st, x+dot*2+6*s, top+L.statY*s, int(12*s), 400, th.muted, alignLeft)
	}
	upd := time.Now()
	if len(vs) > 0 {
		upd = vs[0].Updated
	}
	c.text("Updated "+upd.Format("15:04"), ox+W-16*s, oy+H-24*s, int(11*s), 400, th.muted, alignRight)
	flyHits = nil
	lw := c.text("Settings", ox+18*s, oy+H-25*s, int(12*s), 600, accentText(th), alignLeft)
	flyHits = append(flyHits, hit{ox + 10*s, oy + H - 30*s, ox + 26*s + float64(lw), oy + H - 4*s, func() {
		hideFlyout()
		showSettings()
	}})
	return c
}

func partColor(p Part) color {
	if p.Level < 0 {
		return colGrey
	}
	return batteryColor(p.Level, p.Charging)
}

func toggleFlyout() {
	if flyVisible {
		hideFlyout()
		return
	}
	if time.Since(flyHidden) < 350*time.Millisecond { // the click that closed it
		return
	}
	showFlyout()
}

// placeNearTray positions a w×h panel (plus shadow padding) next to the
// tray, wherever the taskbar is. Returns window position and DPI scale.
func placeNearTray(render func(s float64) *canvas) (*canvas, int, int, float64) {
	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	work, s := monitorAt(pt, false)
	c := render(s)
	pad := int(shadowPad * s)
	m := int(12 * s)
	w, h := c.w-2*pad, c.h-2*pad
	x := int(pt.X) - w/2
	y := int(work.Bottom) - h - m
	switch {
	case pt.Y < work.Top:
		y = int(work.Top) + m
	case pt.Y >= work.Top && pt.Y < work.Bottom: // side taskbar
		y = int(pt.Y) - h/2
		if pt.X >= work.Right {
			x = int(work.Right) - w - m
		} else if pt.X < work.Left {
			x = int(work.Left) + m
		}
	}
	x = max(int(work.Left)+m, min(x, int(work.Right)-w-m))
	y = max(int(work.Top)+m, min(y, int(work.Bottom)-h-m))
	return c, x - pad, y - pad, s
}

func showFlyout() {
	c, x, y, s := placeNearTray(func(s float64) *canvas { return renderFlyout(currentViews(), s) })
	flyScale, flyX, flyY, lastFlyH = s, x, y, c.h
	c.present(flyHwnd, flyX, flyY, 255)
	pShowWindow.Call(flyHwnd, SW_SHOW)
	pSetForegroundWindow.Call(flyHwnd)
	flyVisible = true
}

func refreshFlyout() {
	if flyVisible {
		if c := renderFlyout(currentViews(), flyScale); c.h != lastFlyH {
			hideFlyout() // size changed (device added/removed): reopen next click
		} else {
			c.present(flyHwnd, flyX, flyY, 255)
		}
	}
}

func hideFlyout() {
	pShowWindow.Call(flyHwnd, SW_HIDE)
	flyVisible = false
	flyHidden = time.Now()
}

func flyoutProc(h, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_ACTIVATE:
		if wp&0xFFFF == 0 && flyVisible {
			hideFlyout()
		}
		return 0
	case WM_KEYDOWN:
		if wp == 0x1B { // Esc
			hideFlyout()
		}
		return 0
	case WM_LBUTTONUP:
		clickHits(flyHits, lp)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
	return r
}

// ---- toast (silent, small, bottom-right, fades) ----

var (
	toastCanvas *canvas
	toastX      int
	toastY      int
	toastAlpha  int
	toastPhase  int // 0 hidden, 1 fading in, 2 holding, 3 fading out
)

const (
	tmFade = 10
	tmHold = 11
)

func showToast(title, sub string, frac float64, col color) {
	work, s := monitorAt(POINT{}, true)
	th := currentTheme()
	W, H := 292*s, 68*s
	c, ox, oy := card(W, H, s, th)
	cx, cy := ox+34*s, oy+H/2
	c.ring(cx, cy, 15*s, 4.5*s, 1, th.track)
	c.ring(cx, cy, 15*s, 4.5*s, frac, col)
	c.text(title, ox+62*s, oy+14*s, int(14*s), 600, th.fg, alignLeft)
	c.text(sub, ox+62*s, oy+36*s, int(12*s), 400, th.muted, alignLeft)
	pad := int(shadowPad * s)
	m := int(12 * s)
	toastCanvas = c
	toastX = int(work.Right) - int(W) - m - pad
	toastY = int(work.Bottom) - int(H) - m - pad
	toastAlpha, toastPhase = 0, 1
	c.present(toastHwnd, toastX, toastY, 0)
	pShowWindow.Call(toastHwnd, SW_SHOWNOACTIVATE)
	pKillTimer.Call(toastHwnd, tmHold)
	pSetTimer.Call(toastHwnd, tmFade, 15, 0)
}

func toastProc(h, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_TIMER:
		switch wp {
		case tmFade:
			if toastPhase == 1 {
				toastAlpha = min(255, toastAlpha+32)
				if toastAlpha == 255 {
					toastPhase = 2
					pKillTimer.Call(h, tmFade)
					pSetTimer.Call(h, tmHold, 6000, 0)
				}
			} else if toastPhase == 3 {
				toastAlpha = max(0, toastAlpha-24)
				if toastAlpha == 0 {
					toastPhase = 0
					pKillTimer.Call(h, tmFade)
					pShowWindow.Call(h, SW_HIDE)
					toastCanvas = nil
					return 0
				}
			}
			toastCanvas.present(h, toastX, toastY, uint8(toastAlpha))
		case tmHold:
			pKillTimer.Call(h, tmHold)
			toastPhase = 3
			pSetTimer.Call(h, tmFade, 15, 0)
		}
		return 0
	case WM_LBUTTONUP:
		if toastPhase == 1 || toastPhase == 2 {
			pKillTimer.Call(h, tmHold)
			toastPhase = 3
			pSetTimer.Call(h, tmFade, 15, 0)
		}
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
	return r
}
