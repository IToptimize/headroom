//go:build windows

// Headroom: a tiny tray app showing the battery of wireless headsets (USB
// dongles over HID, Bluetooth, AirPods). Talks to the devices directly.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appName      = "Headroom"
	runValue     = "Headroom"
	runKey       = `Software\Microsoft\Windows\CurrentVersion\Run`
	pollInterval = 60 * time.Second
	fastInterval = 15 * time.Second
	fastWindow   = 2 * time.Minute

	wmTray    = WM_APP + 1
	wmUpdated = WM_APP + 2
	wmReport  = WM_APP + 3

	idRefresh  = 1
	idStartup  = 2
	idExit     = 3
	idSettings = 4
	idReport   = 5
)

var (
	hwnd           uintptr
	taskbarCreated uintptr
	curIcon        uintptr
	mu             sync.Mutex
	latest         []Reading
	havePoll       bool
	trackers       = map[string]*tracker{}
	views          []View
	notified       = map[string]map[int]bool{}
	pollNow        = make(chan struct{}, 1)
	debug          bool
)

func dbg(f string, a ...any) {
	line := fmt.Sprintf(f, a...)
	remember(line) // kept in memory for "Report my headset"
	if debug {
		log.Print(line)
	}
}

func main() {
	for _, a := range os.Args[1:] {
		if a == "--statusline" { // called by Claude Code, not a tray instance
			runStatusLine()
			return
		}
	}
	runtime.LockOSThread()
	loadConfig()
	for _, a := range os.Args[1:] {
		if a == "--debug" {
			debug = true
			exe, _ := os.Executable()
			if f, err := os.Create(filepath.Join(filepath.Dir(exe), "headroom.log")); err == nil {
				log.SetOutput(f)
			}
		}
	}

	// single instance
	mutexName := uintptr(unsafe.Pointer(u16("Local\\HeadroomTray")))
	mutex, _, e := pCreateMutexW.Call(0, 0, mutexName)
	if e == syscall.ERROR_ALREADY_EXISTS {
		// another copy (maybe an older version) is running: close it and take over
		syscall.CloseHandle(syscall.Handle(mutex))
		if w, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16("HeadroomWnd"))), 0); w != 0 {
			pPostMessageW.Call(w, 0x0010 /*WM_CLOSE*/, 0, 0)
		}
		for i := 0; ; i++ {
			time.Sleep(200 * time.Millisecond)
			mutex, _, e = pCreateMutexW.Call(0, 0, mutexName)
			if e != syscall.ERROR_ALREADY_EXISTS {
				break
			}
			syscall.CloseHandle(syscall.Handle(mutex))
			if i > 25 {
				return
			}
		}
	}
	closeLegacyInstance()
	mig := migrateLegacy()
	if firstRunSetup(mutex, mig) {
		return // the installed copy has taken over
	}

	if pSetProcessDpiAwarenessContext.Find() == nil {
		pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2 (-4)
	} else {
		pSetProcessDPIAware.Call()
	}

	hinst, _, _ := pGetModuleHandleW.Call(0)
	cls := u16("HeadroomWnd")
	appIcon, _, _ := pLoadIconW.Call(hinst, 1) // the embedded app icon
	wc := WNDCLASSEXW{
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hinst,
		HIcon:         appIcon,
		LpszClassName: cls,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(ptr(&wc))
	hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16(appName))),
		0, 0, 0, 0, 0, 0, 0, hinst, 0)
	taskbarCreated, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))

	initPopups()
	addIcon()
	if startupEnabled() {
		autoPinOnce() // first run after choosing "start with Windows": keep the icon visible
	}
	cleanupLegacy()
	pSetTimer.Call(hwnd, tmClaude, 5000, 0) // pick up Claude Code usage updates quickly
	go pollLoop()

	var m MSG
	for {
		r, _, _ := pGetMessageW.Call(ptr(&m), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(ptr(&m))
		pDispatchMessageW.Call(ptr(&m))
	}
}

// pollLoop checks every 15 s for two minutes after anything changes
// (start, device added/removed, charger in/out, headset on/off, manual
// refresh), otherwise every 60 s.
func pollLoop() {
	var p Poller
	prevSig := ""
	fastUntil := time.Now().Add(fastWindow)
	for i := 0; ; i++ {
		rs := p.Poll()
		sig := ""
		for _, r := range rs {
			sig += fmt.Sprintf("%s/%v/%d;", r.ID, r.OK, r.Charging)
		}
		if i > 0 && sig != prevSig {
			fastUntil = time.Now().Add(fastWindow)
			dbg("state changed: fast polling")
		}
		prevSig = sig
		mu.Lock()
		latest, havePoll = rs, true
		mu.Unlock()
		pPostMessageW.Call(hwnd, wmUpdated, 0, 0)
		wait := pollEvery()
		if time.Now().Before(fastUntil) {
			wait = fastInterval
		}
		select {
		case <-pollNow:
			fastUntil = time.Now().Add(fastWindow)
		case <-time.After(wait):
		}
	}
}

func requestPoll() {
	select {
	case pollNow <- struct{}{}:
	default:
	}
}

func currentViews() []View { return views }

// trayView picks the device shown in the tray: the one chosen in the menu if
// it is connected, else the first connected device, else the first device.
func trayView() (View, bool) {
	if len(views) == 0 {
		return View{}, false
	}
	pin := regGet("TrayDevice")
	for _, v := range views {
		if v.ID == pin && v.OK && v.ID != "claude" {
			return v, true
		}
	}
	for _, v := range views {
		if v.OK && v.ID != "claude" {
			return v, true
		}
	}
	if views[0].ID == "claude" {
		return View{}, false
	}
	return views[0], true
}

func wndProc(h, msg, wp, lp uintptr) uintptr {
	switch {
	case msg == wmTray:
		switch lp & 0xFFFF {
		case WM_LBUTTONUP:
			toggleFlyout()
			requestPoll()
		case WM_RBUTTONUP, WM_CONTEXTMENU:
			showMenu()
		}
		return 0
	case msg == wmUpdated:
		onUpdate()
		return 0
	case msg == wmReport:
		finishReport()
		return 0
	case msg == taskbarCreated && taskbarCreated != 0:
		addIcon()
		claudeIconShown = false
		renderClaudeIcon()
		return 0
	case msg == WM_TIMER && wp == tmClaude:
		checkClaudeFile()
		return 0
	case msg == WM_DESTROY:
		nid := baseNID()
		pShellNotifyIconW.Call(NIM_DELETE, ptr(&nid))
		removeClaudeIcon()
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
	return r
}

func baseNID() NOTIFYICONDATAW {
	var n NOTIFYICONDATAW
	n.CbSize = uint32(unsafe.Sizeof(n))
	n.HWnd = hwnd
	n.UID = 1
	return n
}

func setIcon(ic uintptr) {
	if curIcon != 0 {
		pDestroyIcon.Call(curIcon)
	}
	curIcon = ic
}

func iconAndTip() string {
	mu.Lock()
	ok := havePoll
	mu.Unlock()
	tv, _ := trayView()
	setIcon(makeTrayIcon(tv, !ok))
	if !ok {
		return appName + "\nChecking…"
	}
	return tooltipAll(views)
}

func addIcon() {
	n := baseNID()
	n.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	n.UCallbackMessage = wmTray
	copyU16(n.SzTip[:], iconAndTip())
	n.HIcon = curIcon
	pShellNotifyIconW.Call(NIM_ADD, ptr(&n))
}

func onUpdate() {
	mu.Lock()
	rs := latest
	mu.Unlock()
	now := time.Now()
	vs := make([]View, 0, len(rs))
	for _, r := range rs {
		t := trackers[r.ID]
		if t == nil {
			t = newTracker(r.ID)
			trackers[r.ID] = t
		}
		v := t.update(r, now)
		dbg("view %+v", v)
		vs = append(vs, v)
	}
	if cv, ok := claudeView(now); ok && cfg.ClaudePanel {
		vs = append(vs, cv)
	}
	views = vs
	render()
	for _, v := range views {
		checkThresholds(v)
	}
}

// render refreshes the tray icon, tooltip and open flyout from views.
func render() {
	n := baseNID()
	n.UFlags = NIF_ICON | NIF_TIP
	copyU16(n.SzTip[:], iconAndTip())
	n.HIcon = curIcon
	pShellNotifyIconW.Call(NIM_MODIFY, ptr(&n))
	renderClaudeIcon()
	refreshFlyout()
}

// checkThresholds pops a small silent notice when a device crosses 50/10/5 %.
func checkThresholds(v View) {
	if !v.OK || v.Level < 0 || v.ID == "claude" {
		return
	}
	nd, seen := notified[v.ID]
	if !seen {
		nd = map[int]bool{}
		notified[v.ID] = nd
	}
	if v.Charging || v.Full {
		for _, t := range popupLevels {
			nd[t] = false
		}
		return
	}
	if !seen { // don't announce thresholds we were already below when first seen
		for _, t := range popupLevels {
			nd[t] = v.Level <= t
		}
		return
	}
	hit := -1
	for _, t := range popupLevels {
		if !cfg.Popups[t] {
			continue
		}
		if v.Level > t+5 {
			nd[t] = false
		}
		if v.Level <= t && !nd[t] {
			nd[t] = true
			hit = t
		}
	}
	if hit < 0 {
		return
	}
	title := fmt.Sprintf("%s at %d%%", shortName(v.Name), v.Level)
	sub := leftText(v)
	if hit <= 10 {
		sub = "Time to charge · " + sub
	}
	showToast(title, sub, float64(v.Level)/100, levelColor(v))
}

const idDeviceBase = 100

func showMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(flags uintptr, id uintptr, text string) {
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(u16(text))))
	}
	vs := views
	pin := regGet("TrayDevice")
	if len(vs) == 0 {
		add(MF_STRING|MF_GRAYED, 0, "No headset found")
	} else {
		add(MF_STRING|MF_GRAYED, 0, "Show in tray:")
		fl := uintptr(MF_STRING)
		if pin == "" {
			fl |= MF_CHECKED
		}
		add(fl, idDeviceBase, "   Automatic")
		for i, v := range vs {
			fl := uintptr(MF_STRING)
			if v.ID == pin {
				fl |= MF_CHECKED
			}
			label := "   " + shortName(v.Name)
			if v.OK && v.Level >= 0 {
				label += fmt.Sprintf("  (%d%%)", v.Level)
			} else if !v.OK {
				label += "  (off)"
			}
			add(fl, uintptr(idDeviceBase+1+i), label)
		}
	}
	add(MF_SEPARATOR, 0, "")
	add(MF_STRING, idRefresh, "Refresh now")
	fl := uintptr(MF_STRING)
	if startupEnabled() {
		fl |= MF_CHECKED
	}
	add(fl, idStartup, "Start with Windows")
	add(MF_STRING, idSettings, "Settings…")
	add(MF_STRING, idReport, "Report my headset…")
	add(MF_SEPARATOR, 0, "")
	add(MF_STRING, idExit, "Exit")

	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	pSetForegroundWindow.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, TPM_RIGHTBUTTON|TPM_RETURNCMD|TPM_NONOTIFY, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	switch {
	case cmd == idRefresh:
		requestPoll()
	case cmd == idStartup:
		setStartup(!startupEnabled())
	case cmd == idExit:
		pDestroyWindow.Call(hwnd)
	case cmd == idSettings:
		showSettings()
	case cmd == idReport:
		createReport()
	case cmd == idDeviceBase:
		regSet("TrayDevice", "")
		render()
	case cmd > idDeviceBase && int(cmd-idDeviceBase-1) < len(vs):
		regSet("TrayDevice", vs[cmd-idDeviceBase-1].ID)
		render()
	}
}

func startupEnabled() bool {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(runKey))), 0, KEY_READ, ptr(&k)); r != 0 {
		return false
	}
	defer pRegCloseKey.Call(k)
	r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(u16(runValue))), 0, 0, 0, 0)
	return r == 0
}

func setStartup(on bool) {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(runKey))), 0, KEY_SET_VALUE, ptr(&k)); r != 0 {
		return
	}
	defer pRegCloseKey.Call(k)
	name := uintptr(unsafe.Pointer(u16(runValue)))
	if !on {
		pRegDeleteValueW.Call(k, name)
		return
	}
	exe, _ := os.Executable()
	setStartupPath(exe)
}

func setStartupPath(exe string) {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(runKey))), 0, KEY_SET_VALUE, ptr(&k)); r != 0 {
		return
	}
	defer pRegCloseKey.Call(k)
	name := uintptr(unsafe.Pointer(u16(runValue)))
	v := syscall.StringToUTF16(`"` + exe + `"`)
	pRegSetValueExW.Call(k, name, 0, REG_SZ, uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2))
}
