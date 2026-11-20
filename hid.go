//go:build windows

package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// chargeState tracks voltage history so charging can be detected by the
// jump the charger causes (~+125 mV on the Cloud Flight) instead of a fixed cutoff.
type chargeState struct {
	restMv   int // last voltage while on battery
	peakMv   int // highest voltage seen while charging
	entryMv  int // voltage when charging was detected
	since    time.Time
	charging bool
	tid      uint16 // transaction counter (Sony INZONE)
	parts    []Part // per-battery detail set by some protocols (Nova Pro spare)
}

const (
	chargeJump   = 40   // mV rise over resting voltage = charger plugged in
	unplugDrop   = 60   // mV fall from charging peak = charger removed
	chargeNoBase = 4140 // without a resting baseline (app started while charging)
	chargeAbsMax = 4195 // above this it can only be charging
)

func (st *chargeState) update(mv int) bool {
	if st.charging {
		st.peakMv = max(st.peakMv, mv)
		unplugged := mv <= st.peakMv-unplugDrop || (st.restMv == 0 && mv < chargeNoBase-unplugDrop)
		// a charger always pushes the voltage up; flat for 10 min = false alarm
		flat := time.Since(st.since) > 10*time.Minute && st.peakMv-st.entryMv < 8 && mv < chargeNoBase
		if unplugged || flat {
			st.charging = false
			st.restMv = mv
		}
		return st.charging
	}
	if (st.restMv > 0 && mv-st.restMv >= chargeJump) || (st.restMv == 0 && mv > chargeNoBase) || mv > chargeAbsMax {
		st.charging, st.peakMv, st.entryMv, st.since = true, mv, mv, time.Now()
		return true
	}
	st.restMv = mv
	return false
}

// flightLevel maps millivolts to % with a standard single-cell Li-ion/LiPo
// discharge curve (linear between points). The old HeadsetControl curve
// clipped everything above 3.975 V to 100 %, which hid the top ~20 %.
var lipoCurve = [][2]int{
	{3300, 0}, {3610, 5}, {3690, 10}, {3710, 15}, {3730, 20}, {3750, 25},
	{3770, 30}, {3790, 35}, {3800, 40}, {3820, 45}, {3840, 50}, {3850, 55},
	{3870, 60}, {3910, 65}, {3950, 70}, {3980, 75}, {4020, 80}, {4080, 85},
	{4110, 90}, {4150, 95}, {4180, 100},
}

func flightLevel(mv int) int {
	if mv <= lipoCurve[0][0] {
		return 0
	}
	for i := 1; i < len(lipoCurve); i++ {
		a, b := lipoCurve[i-1], lipoCurve[i]
		if mv <= b[0] {
			return a[1] + (mv-a[0])*(b[1]-a[1])/(b[0]-a[0])
		}
	}
	return 100
}

type hidDev struct {
	h                      syscall.Handle
	path                   string
	inLen, outLen, featLen int
	usage                  uint16
	ev                     syscall.Handle
}

// ifaceOf returns the USB interface number from a HID path ("&mi_03"), or -1.
func ifaceOf(path string) int {
	p := strings.ToLower(path)
	i := strings.Index(p, "&mi_")
	if i < 0 || len(p) < i+6 {
		return -1
	}
	n := 0
	for _, c := range p[i+4 : i+6] {
		switch {
		case c >= '0' && c <= '9':
			n = n*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			n = n*16 + int(c-'a'+10)
		default:
			return -1
		}
	}
	return n
}

func (d *hidDev) close() {
	if d.h != 0 && d.h != syscall.InvalidHandle {
		syscall.CloseHandle(d.h)
	}
	if d.ev != 0 {
		syscall.CloseHandle(d.ev)
	}
	d.h, d.ev = 0, 0
}

var errTimeout = errors.New("timeout")

// enumHID returns the device paths of all present HID interfaces.
func enumHID() []string {
	var g GUID
	pHidD_GetHidGuid.Call(ptr(&g))
	set, _, _ := pSetupDiGetClassDevsW.Call(ptr(&g), 0, 0, DIGCF_PRESENT|DIGCF_DEVICEINTERFACE)
	if set == 0 || set == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(set)

	var paths []string
	for i := uintptr(0); ; i++ {
		var did SP_DEVICE_INTERFACE_DATA
		did.CbSize = uint32(unsafe.Sizeof(did))
		r, _, _ := pSetupDiEnumDeviceInterfaces.Call(set, 0, ptr(&g), i, ptr(&did))
		if r == 0 {
			break
		}
		buf := make([]byte, 2048)
		// cbSize of SP_DEVICE_INTERFACE_DETAIL_DATA_W: 8 on 64-bit, 6 on 32-bit
		if unsafe.Sizeof(uintptr(0)) == 8 {
			*(*uint32)(unsafe.Pointer(&buf[0])) = 8
		} else {
			*(*uint32)(unsafe.Pointer(&buf[0])) = 6
		}
		r, _, _ = pSetupDiGetDeviceInterfaceDetailW.Call(set, ptr(&did), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
		if r == 0 {
			continue
		}
		u := (*[1000]uint16)(unsafe.Pointer(&buf[4]))
		paths = append(paths, syscall.UTF16ToString(u[:]))
	}
	return paths
}

func openHID(path string, access uint32) (syscall.Handle, error) {
	return syscall.CreateFile(u16(path), access,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
}

func (d *hidDev) overlappedIO(write bool, buf []byte, timeout time.Duration) (int, error) {
	ov := new(syscall.Overlapped)
	ov.HEvent = d.ev
	var n uint32
	var err error
	if write {
		err = syscall.WriteFile(d.h, buf, &n, ov)
	} else {
		err = syscall.ReadFile(d.h, buf, &n, ov)
	}
	if err != nil && err != syscall.ERROR_IO_PENDING {
		return 0, err
	}
	w, _, _ := pWaitForSingleObject.Call(uintptr(d.ev), uintptr(timeout.Milliseconds()))
	if w != WAIT_OBJECT_0 {
		pCancelIoEx.Call(uintptr(d.h), uintptr(unsafe.Pointer(ov)))
		pGetOverlappedResult.Call(uintptr(d.h), uintptr(unsafe.Pointer(ov)), ptr(&n), 1)
		return 0, errTimeout
	}
	r, _, e := pGetOverlappedResult.Call(uintptr(d.h), uintptr(unsafe.Pointer(ov)), ptr(&n), 0)
	if r == 0 {
		return 0, e
	}
	return int(n), nil
}

func (d *hidDev) send(pkt []byte) error {
	if d.outLen > 0 {
		buf := make([]byte, d.outLen)
		copy(buf, pkt)
		_, err := d.overlappedIO(true, buf, time.Second)
		if err == nil {
			return nil
		}
		dbg("  write failed: %v", err)
	}
	if d.featLen > 0 { // some dongles only take feature reports on Windows
		buf := make([]byte, d.featLen)
		copy(buf, pkt)
		if r, _, e := pHidD_SetFeature.Call(uintptr(d.h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); r == 0 {
			return fmt.Errorf("setfeature: %v", e)
		}
		return nil
	}
	return errors.New("interface takes no output")
}

func openRW(path string) *hidDev {
	h, err := openHID(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE)
	if err != nil {
		dbg("  open rw failed: %v", err)
		return nil
	}
	d := &hidDev{h: h, path: path}
	var pp uintptr
	if r, _, _ := pHidD_GetPreparsedData.Call(uintptr(h), ptr(&pp)); r != 0 {
		var caps HIDP_CAPS
		if s, _, _ := pHidP_GetCaps.Call(pp, ptr(&caps)); s == HIDP_STATUS_SUCCESS {
			d.inLen, d.outLen, d.featLen = int(caps.InputReportByteLength), int(caps.OutputReportByteLength), int(caps.FeatureReportByteLength)
			d.usage = caps.UsagePage
			dbg("  %s usage %04x:%04x in=%d out=%d feat=%d", shortPath(path), caps.UsagePage, caps.Usage, d.inLen, d.outLen, d.featLen)
		}
		pHidD_FreePreparsedData.Call(pp)
	}
	ev, _, _ := pCreateEventW.Call(0, 1, 0, 0)
	d.ev = syscall.Handle(ev)
	return d
}

func shortPath(p string) string {
	if i := strings.Index(p, "#{"); i > 0 {
		p = p[:i]
	}
	return strings.TrimPrefix(p, `\\?\`)
}

// query sends pkt and waits for a report accepted by match.
func (d *hidDev) query(prep bool, pkt []byte, match func([]byte) bool) ([]byte, error) {
	if d.h == 0 || d.inLen == 0 {
		return nil, errors.New("not usable")
	}
	pHidD_FlushQueue.Call(uintptr(d.h)) // drop stale/unsolicited reports
	if prep {
		b := make([]byte, d.inLen)
		b[0] = pkt[0]
		pHidD_GetInputReport.Call(uintptr(d.h), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	}
	if err := d.send(pkt); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(1200 * time.Millisecond)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, errTimeout
		}
		buf := make([]byte, d.inLen)
		n, err := d.overlappedIO(false, buf, left)
		if err != nil {
			return nil, err
		}
		r := buf[:n]
		if len(r) > 0 && r[0] == 0 { // no report ID: drop the 0 like hidapi does
			r = r[1:]
		}
		dbg("  rx % x", r[:min(len(r), 24)])
		if match(r) {
			return r, nil
		}
	}
}

// ---- HID backend: one entry per supported dongle ----

type dongle struct {
	key    string // vid:pid
	proto  *protocol
	paths  []string
	active *hidDev
	st     chargeState
	lastOK time.Time
}

type hidBackend struct {
	dongles map[string]*dongle
}

func (b *hidBackend) poll() []Reading {
	if b.dongles == nil {
		b.dongles = map[string]*dongle{}
	}
	// find present dongles (attribute query needs no access rights)
	seen := map[string][]string{}
	protos := map[string]*protocol{}
	for _, path := range enumHID() {
		h, err := openHID(path, 0)
		if err != nil {
			continue
		}
		var a HIDD_ATTRIBUTES
		a.Size = uint32(unsafe.Sizeof(a))
		r, _, _ := pHidD_GetAttributes.Call(uintptr(h), ptr(&a))
		syscall.CloseHandle(h)
		if r == 0 {
			continue
		}
		p := findProtocol(a.VendorID, a.ProductID)
		if p == nil {
			continue
		}
		key := fmt.Sprintf("%04x:%04x", a.VendorID, a.ProductID)
		seen[key] = append(seen[key], path)
		protos[key] = p
	}
	for key, d := range b.dongles { // unplugged
		if _, ok := seen[key]; !ok {
			if d.active != nil {
				d.active.close()
			}
			delete(b.dongles, key)
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Reading
	for _, key := range keys {
		d := b.dongles[key]
		if d == nil {
			d = &dongle{key: key, proto: protos[key]}
			b.dongles[key] = d
			dbg("found %s (%s)", key, d.proto.name)
		}
		d.paths = seen[key]
		out = append(out, d.poll())
	}
	return out
}

func (d *dongle) read(dev *hidDev) (int, int, error) {
	// After the headset was off/away for a while its resting voltage has
	// recovered, so the old baseline would look like a charger: start over.
	if !d.lastOK.IsZero() && time.Since(d.lastOK) > 2*time.Minute {
		dbg("%s back after %v: resetting charge baseline", d.key, time.Since(d.lastOK).Round(time.Second))
		d.st = chargeState{}
	}
	q := func(p []byte, m func([]byte) bool) ([]byte, error) { return dev.query(d.proto.prep, p, m) }
	lvl, ch, err := d.proto.read(q, &d.st)
	if err == nil {
		d.lastOK = time.Now()
	}
	return lvl, ch, err
}

func (d *dongle) poll() Reading {
	r := Reading{ID: "hid:" + d.key, Name: d.proto.name, Kind: "USB", Tested: d.proto.tested, Dongle: true}
	if d.active == nil {
		// probe each interface of the dongle until one answers
		for _, path := range d.paths {
			if d.proto.mi > 0 && ifaceOf(path) != d.proto.mi-1 && ifaceOf(path) >= 0 {
				continue
			}
			dev := openRW(path)
			if dev == nil {
				continue
			}
			if d.proto.usage != 0 && dev.usage != d.proto.usage {
				dev.close()
				continue
			}
			lvl, ch, err := d.read(dev)
			if err == nil || err == errOffline {
				d.active = dev
				dbg("using %s", shortPath(path))
				return d.finish(r, lvl, ch, err)
			}
			dbg("  probe: %v", err)
			dev.close()
		}
		r.Err = "Headset off or out of range"
		return r
	}
	lvl, ch, err := d.read(d.active)
	if err != nil && err != errOffline && err != errTimeout {
		dbg("read failed, re-probing: %v", err)
		d.active.close()
		d.active = nil
	}
	return d.finish(r, lvl, ch, err)
}

func (d *dongle) finish(r Reading, lvl, ch int, err error) Reading {
	if err != nil {
		r.Err = "Headset off or out of range"
		return r
	}
	r.OK, r.Level, r.Charging = true, min(lvl, 100), ch
	r.Parts = d.st.parts
	dbg("%s level=%d charging=%d", d.key, r.Level, r.Charging)
	return r
}
