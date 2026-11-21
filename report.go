//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// "Report my headset": a plain-text report with what the app can see, for
// testers to send when a headset isn't found or reads wrong.

var (
	logMu   sync.Mutex
	logRing []string // last log lines, kept even without --debug

	ntdll                   = syscall.NewLazyDLL("ntdll.dll")
	pRtlGetVersion          = ntdll.NewProc("RtlGetVersion")
	pHidD_GetProductString  = hiddll.NewProc("HidD_GetProductString")
	pHidD_GetManufacturerSt = hiddll.NewProc("HidD_GetManufacturerString")
	pOpenClipboard          = user32.NewProc("OpenClipboard")
	pEmptyClipboard         = user32.NewProc("EmptyClipboard")
	pSetClipboardData       = user32.NewProc("SetClipboardData")
	pCloseClipboard         = user32.NewProc("CloseClipboard")
	pGlobalAlloc            = kernel32.NewProc("GlobalAlloc")
	pGlobalLock             = kernel32.NewProc("GlobalLock")
	pGlobalUnlock           = kernel32.NewProc("GlobalUnlock")
	pRtlMoveMemory          = kernel32.NewProc("RtlMoveMemory")
)

func remember(line string) {
	logMu.Lock()
	logRing = append(logRing, time.Now().Format("15:04:05 ")+line)
	if len(logRing) > 400 {
		logRing = logRing[len(logRing)-400:]
	}
	logMu.Unlock()
}

func windowsBuild() string {
	var v struct {
		size, major, minor, build, platform uint32
		csd                                 [128]uint16
	}
	v.size = uint32(unsafe.Sizeof(v))
	pRtlGetVersion.Call(ptr(&v))
	return fmt.Sprintf("%d.%d build %d", v.major, v.minor, v.build)
}

func hidString(p *syscall.LazyProc, h syscall.Handle) string {
	buf := make([]uint16, 128)
	if r, _, _ := p.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2)); r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// hidInventory lists every HID device once per VID:PID, with names and the
// interfaces/usage pages it exposes.
func hidInventory() []string {
	type dev struct {
		name   string
		ifaces map[string]bool
	}
	devs := map[string]*dev{}
	for _, path := range enumHID() {
		h, err := openHID(path, 0)
		if err != nil {
			continue
		}
		var a HIDD_ATTRIBUTES
		a.Size = uint32(unsafe.Sizeof(a))
		if r, _, _ := pHidD_GetAttributes.Call(uintptr(h), ptr(&a)); r == 0 {
			syscall.CloseHandle(h)
			continue
		}
		key := fmt.Sprintf("%04x:%04x", a.VendorID, a.ProductID)
		d := devs[key]
		if d == nil {
			d = &dev{ifaces: map[string]bool{}}
			devs[key] = d
		}
		if d.name == "" {
			d.name = strings.TrimSpace(hidString(pHidD_GetManufacturerSt, h) + " " + hidString(pHidD_GetProductString, h))
		}
		page := ""
		var pp uintptr
		if r, _, _ := pHidD_GetPreparsedData.Call(uintptr(h), ptr(&pp)); r != 0 {
			var caps HIDP_CAPS
			if s, _, _ := pHidP_GetCaps.Call(pp, ptr(&caps)); s == HIDP_STATUS_SUCCESS {
				page = fmt.Sprintf(" page %04x in=%d out=%d feat=%d", caps.UsagePage, caps.InputReportByteLength, caps.OutputReportByteLength, caps.FeatureReportByteLength)
			}
			pHidD_FreePreparsedData.Call(pp)
		}
		d.ifaces[fmt.Sprintf("mi=%d%s", ifaceOf(path), page)] = true
		syscall.CloseHandle(h)
	}
	keys := make([]string, 0, len(devs))
	for k := range devs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		d := devs[k]
		var vid, pid uint16
		fmt.Sscanf(k, "%04x:%04x", &vid, &pid)
		tag := ""
		if p := findProtocol(vid, pid); p != nil {
			tag = "  [supported: " + p.name + "]"
		}
		out = append(out, fmt.Sprintf("%s  %s%s", k, d.name, tag))
		ifs := make([]string, 0, len(d.ifaces))
		for i := range d.ifaces {
			ifs = append(ifs, i)
		}
		sort.Strings(ifs)
		for _, i := range ifs {
			out = append(out, "      "+i)
		}
	}
	return out
}

func buildReport() string {
	var b strings.Builder
	w := func(f string, a ...any) { fmt.Fprintf(&b, f+"\r\n", a...) }
	w("%s %s report - %s", appName, appVersion, time.Now().Format("2006-01-02 15:04"))
	w("Windows %s", windowsBuild())
	w("")
	w("== What %s shows ==", appName)
	if len(views) == 0 {
		w("(no devices)")
	}
	for _, v := range views {
		state := v.Err
		if v.OK {
			state = fmt.Sprintf("%d%% charging=%v", v.Level, v.Charging)
		}
		w("%s [%s] %s tested=%v  %s", v.Name, v.ID, v.Kind, v.Tested, state)
		for _, p := range v.Parts {
			w("    %s %d%% charging=%v %s", p.Label, p.Level, p.Charging, p.Note)
		}
	}
	w("")
	w("== USB / HID devices (VID:PID) ==")
	for _, l := range hidInventory() {
		w("%s", l)
	}
	w("")
	w("== Bluetooth ==")
	btMu.Lock()
	bt := append([]string(nil), btInventory...)
	btMu.Unlock()
	if len(bt) == 0 {
		w("(none seen)")
	}
	for _, l := range bt {
		w("%s", l)
	}
	w("")
	w("== Recent log ==")
	logMu.Lock()
	for _, l := range logRing {
		w("%s", l)
	}
	logMu.Unlock()
	return b.String()
}

func copyToClipboard(s string) {
	u := syscall.StringToUTF16(s)
	h, _, _ := pGlobalAlloc.Call(0x0002 /*GMEM_MOVEABLE*/, uintptr(len(u)*2))
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	pRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)*2))
	pGlobalUnlock.Call(h)
	if r, _, _ := pOpenClipboard.Call(hwnd); r == 0 {
		return
	}
	pEmptyClipboard.Call()
	pSetClipboardData.Call(13 /*CF_UNICODETEXT*/, h)
	pCloseClipboard.Call()
}

// createReport saves the report next to the app, copies it to the clipboard
// and opens it in Notepad.
func createReport() {
	msg := "This creates a text file with what Headroom can see on this PC:\n\n" +
		"•  connected USB and Bluetooth devices (names and IDs)\n" +
		"•  battery readings and Headroom's recent log\n" +
		"•  Claude usage numbers, if reading usage is on\n\n" +
		"No passwords or login tokens. Bluetooth addresses are shortened.\n\n" +
		"It's saved next to Headroom, copied to the clipboard and opened in Notepad, " +
		"so you can read it before you share it with anyone."
	if r, _, _ := pMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(u16(msg))),
		uintptr(unsafe.Pointer(u16("Create troubleshooting report"))), 0x1|0x40); r != 1 { // OK/Cancel, info icon
		return
	}
	if cfg.ClaudeFetch {
		forceShapeLog.Store(true)
		kickClaudeFetch()
	}
	remember("--- report requested ---")
	requestPoll()
	go func() {
		time.Sleep(4 * time.Second) // let the refresh finish so the log has a full poll
		pPostMessageW.Call(hwnd, wmReport, 0, 0)
	}()
}

var macRe = regexp.MustCompile(`mac=([0-9A-Fa-f]{8})([0-9A-Fa-f]{4})`)

func finishReport() {
	text := macRe.ReplaceAllString(buildReport(), "mac=…$2") // keep only the last 4
	exe, _ := os.Executable()
	path := filepath.Join(filepath.Dir(exe), "headroom-report.txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		path = filepath.Join(os.TempDir(), "headroom-report.txt")
		os.WriteFile(path, []byte(text), 0o644)
	}
	copyToClipboard(text)
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16("notepad.exe"))), uintptr(unsafe.Pointer(u16(`"`+path+`"`))), 0, 1)
	showToast("Report created", "Copied to clipboard and opened in Notepad", 1, accentText(currentTheme()))
}
