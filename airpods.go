//go:build windows

package main

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

// AirPods (and Beats) don't report battery to Windows, but they broadcast
// Apple's "proximity pairing" BLE advertisement (manufacturer data, company
// 0x004C, type 0x07) with left/right/case battery in 10 % steps. Layout as
// used by OpenPods and AirPodsDesktop:
//
//	[0]=0x07 [1]=0x19 [2]=0x01 [3:5]=model [5]=status [6]=L/R battery nibbles
//	[7]=charging flags (hi nibble) + case battery (lo nibble) [8]=lid
//
// We only listen while AirPods/Beats are connected to this PC, and pick the
// strongest recent broadcast so a neighbour's AirPods aren't shown.

type podsBeacon struct {
	model            uint16
	left, right, cse int // %; -1 unknown
	chL, chR, chCase bool
	rssi             int16
	at               time.Time
}

func podLevel(n byte) int {
	switch {
	case n == 10:
		return 100
	case n < 10:
		return int(n)*10 + 5 // middle of the 10 % bucket
	}
	return -1 // 15 = not available (in closed case / out of range)
}

func parsePods(b []byte) (podsBeacon, bool) {
	if len(b) != 27 || b[0] != 0x07 || b[1] != 0x19 {
		return podsBeacon{}, false
	}
	flip := b[5]&0x20 == 0
	hi, lo := b[6]>>4, b[6]&0x0F
	l, r := lo, hi
	if flip {
		l, r = hi, lo
	}
	flags := b[7] >> 4
	p := podsBeacon{
		model:  uint16(b[3])<<8 | uint16(b[4]),
		left:   podLevel(l),
		right:  podLevel(r),
		cse:    podLevel(b[7] & 0x0F),
		chCase: flags&0x04 != 0,
	}
	if flip {
		p.chL, p.chR = flags&0x02 != 0, flags&0x01 != 0
	} else {
		p.chL, p.chR = flags&0x01 != 0, flags&0x02 != 0
	}
	return p, true
}

var podModels = map[uint16]string{
	0x0220: "AirPods", 0x0F20: "AirPods (2nd gen)", 0x1320: "AirPods (3rd gen)",
	0x1920: "AirPods 4", 0x1B20: "AirPods 4", 0x0E20: "AirPods Pro",
	0x1420: "AirPods Pro 2", 0x2420: "AirPods Pro 2", 0x0A20: "AirPods Max", 0x1F20: "AirPods Max",
}

type podsScanner struct {
	mu      sync.Mutex
	beacons map[uint64]podsBeacon
	running bool
	stop    chan struct{}
	err     string
}

func (s *podsScanner) start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.running = true
	s.beacons = map[uint64]podsBeacon{}
	s.stop = make(chan struct{})
	go s.run(s.stop)
}

func (s *podsScanner) halt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		close(s.stop)
		s.running = false
	}
}

func (s *podsScanner) run(stop chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fail := func(f string, a ...any) {
		s.mu.Lock()
		s.err = fmt.Sprintf(f, a...)
		s.running = false
		s.mu.Unlock()
		dbg("airpods scanner: %s", s.err)
	}
	if err := ole.RoInitialize(1); err != nil {
		dbg("RoInitialize: %v", err) // may already be initialised; carry on
	}
	w, err := advertisement.NewBluetoothLEAdvertisementWatcher()
	if err != nil {
		fail("watcher: %v", err)
		return
	}
	defer w.Release()
	_ = w.SetScanningMode(advertisement.BluetoothLEScanningModePassive)

	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementReceivedEventArgs)
	h := foundation.NewTypedEventHandler(ole.NewGUID(iid), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		s.onAdvert((*advertisement.BluetoothLEAdvertisementReceivedEventArgs)(arg))
	})
	defer h.Release()
	tok, err := w.AddReceived(h)
	if err != nil {
		fail("subscribe: %v", err)
		return
	}
	defer w.RemoveReceived(tok)
	if err := w.Start(); err != nil {
		fail("start: %v (is Bluetooth on?)", err)
		return
	}
	dbg("airpods scanner started")
	<-stop
	_ = w.Stop()
	dbg("airpods scanner stopped")
}

func (s *podsScanner) onAdvert(args *advertisement.BluetoothLEAdvertisementReceivedEventArgs) {
	adv, err := args.GetAdvertisement()
	if err != nil {
		return
	}
	defer adv.Release()
	vec, err := adv.GetManufacturerData()
	if err != nil || vec == nil {
		return
	}
	defer vec.Release()
	n, _ := vec.GetSize()
	for i := uint32(0); i < n; i++ {
		el, err := vec.GetAt(i)
		if err != nil {
			continue
		}
		md := (*advertisement.BluetoothLEManufacturerData)(el)
		if id, _ := md.GetCompanyId(); id == 0x004C {
			if buf, err := md.GetData(); err == nil && buf != nil {
				if data := readBuffer(buf); len(data) > 0 && data[0] == 0x07 {
					if p, ok := parsePods(data); ok {
						p.rssi, _ = args.GetRawSignalStrengthInDBm()
						p.at = time.Now()
						addr, _ := args.GetBluetoothAddress()
						s.mu.Lock()
						s.beacons[addr] = p
						s.mu.Unlock()
					}
				}
				buf.Release()
			}
		}
		md.Release()
	}
}

func readBuffer(buf *streams.IBuffer) []byte {
	size, _ := buf.GetLength()
	if size == 0 || size > 255 {
		return nil
	}
	r, err := streams.DataReaderFromBuffer(buf)
	if err != nil {
		return nil
	}
	defer r.Release()
	b, _ := r.ReadBytes(size)
	return b
}

const (
	podsMaxAge  = 45 * time.Second
	podsMinRSSI = -80
)

// best returns the strongest recent broadcast (ours is normally the closest).
func (s *podsScanner) best() (podsBeacon, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out podsBeacon
	found := false
	for addr, b := range s.beacons {
		if time.Since(b.at) > podsMaxAge {
			delete(s.beacons, addr) // AirPods rotate their address; drop stale ones
			continue
		}
		if b.rssi < podsMinRSSI {
			continue
		}
		if !found || b.rssi > out.rssi {
			out, found = b, true
		}
	}
	return out, found
}

func isApplePods(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "airpods") || strings.Contains(n, "beats") || strings.Contains(n, "powerbeats")
}

// podsReading turns the best broadcast into a device row for a connected
// AirPods/Beats device called name.
func (s *podsScanner) podsReading(name string) Reading {
	r := Reading{ID: "pods:" + name, Name: name, Kind: "Bluetooth", Level: -1, Tested: true}
	b, ok := s.best()
	if !ok {
		r.Err = "Waiting for AirPods battery…"
		s.mu.Lock()
		if s.err != "" {
			r.Err = "Can't read AirPods: " + s.err
		}
		s.mu.Unlock()
		return r
	}
	if name == "" {
		r.Name = podModels[b.model]
	}
	lv := -1
	for _, v := range []int{b.left, b.right} {
		if v >= 0 && (lv < 0 || v < lv) {
			lv = v
		}
	}
	if lv < 0 { // both buds in the case: show the case
		lv = b.cse
	}
	parts := []string{}
	add := func(label string, v int, ch bool) {
		if v < 0 {
			return
		}
		s := fmt.Sprintf("%s %d%%", label, v)
		if ch {
			s += "⚡"
		}
		parts = append(parts, s)
	}
	add("L", b.left, b.chL)
	add("R", b.right, b.chR)
	add("Case", b.cse, b.chCase)
	r.Detail = strings.Join(parts, " · ")
	bud := func(label string, v int, ch bool) Part {
		p := Part{Label: label, Level: v, Charging: ch}
		if v < 0 {
			p.Note = "In case"
		}
		return p
	}
	r.Parts = []Part{bud("Left", b.left, b.chL), bud("Right", b.right, b.chR)}
	// The case only reports with a bud inside and the lid open, so keep the
	// last value it sent and show its age; hide the row until it has reported.
	key := "pods|" + name + "|"
	if b.cse >= 0 {
		regSet(key+"Case", fmt.Sprint(b.cse))
		regSet(key+"CaseAt", fmt.Sprint(time.Now().Unix()))
		r.Parts = append(r.Parts, Part{Label: "Case", Level: b.cse, Charging: b.chCase})
	} else if lv := int(regFloat(key+"Case", -1)); lv >= 0 {
		at := time.Unix(int64(regFloat(key+"CaseAt", 0)), 0)
		note := "as of " + at.Format("15:04")
		if time.Since(at) > 20*time.Hour {
			note = "as of " + at.Format("Jan 2")
		}
		r.Parts = append(r.Parts, Part{Label: "Case", Level: lv, Note: note})
	}
	r.OK = lv >= 0
	r.Level = lv
	budsCharging := (b.left < 0 || b.chL) && (b.right < 0 || b.chR) && (b.left >= 0 || b.right >= 0)
	if budsCharging {
		r.Charging = 1
	}
	if !r.OK {
		r.Err = "AirPods in case"
	}
	dbg("airpods %q model=%04x L=%d R=%d case=%d rssi=%d", name, b.model, b.left, b.right, b.cse, b.rssi)
	return r
}
