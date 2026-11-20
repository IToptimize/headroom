//go:build windows

package main

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Windows 11 keeps one subkey per tray icon under
// HKCU\Control Panel\NotifyIconSettings; IsPromoted=1 shows the icon on the
// taskbar instead of the ^ overflow (the same switch as Settings > Taskbar >
// Other system tray icons). Windows 10 has no such key: we open Settings.

const notifyKey = `Control Panel\NotifyIconSettings`

var (
	pRegEnumKeyExW = advapi32.NewProc("RegEnumKeyExW")
	pShellExecuteW = shell32.NewProc("ShellExecuteW")
)

const REG_DWORD = 4

// ourTrayKeys returns the NotifyIconSettings subkeys that belong to this exe.
func ourTrayKeys() []string {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(notifyKey))), 0, KEY_READ, ptr(&k)); r != 0 {
		return nil
	}
	defer pRegCloseKey.Call(k)
	var out []string
	for i := uintptr(0); ; i++ {
		name := make([]uint16, 256)
		n := uint32(len(name))
		if r, _, _ := pRegEnumKeyExW.Call(k, i, uintptr(unsafe.Pointer(&name[0])), ptr(&n), 0, 0, 0, 0); r != 0 {
			break
		}
		sub := notifyKey + `\` + syscall.UTF16ToString(name[:n])
		var sk uintptr
		if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(sub))), 0, KEY_READ, ptr(&sk)); r != 0 {
			continue
		}
		buf := make([]uint16, 520)
		bn := uint32(len(buf) * 2)
		r, _, _ := pRegQueryValueExW.Call(sk, uintptr(unsafe.Pointer(u16("ExecutablePath"))), 0, 0, uintptr(unsafe.Pointer(&buf[0])), ptr(&bn))
		pRegCloseKey.Call(sk)
		if r == 0 && strings.HasSuffix(strings.ToLower(syscall.UTF16ToString(buf)), `\headroom.exe`) {
			out = append(out, sub)
		}
	}
	return out
}

// trayPinned reports whether our icon is shown on the taskbar (Windows 11).
func trayPinned() bool {
	for _, sub := range ourTrayKeys() {
		var sk uintptr
		if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(sub))), 0, KEY_READ, ptr(&sk)); r != 0 {
			continue
		}
		var v, n uint32 = 0, 4
		r, _, _ := pRegQueryValueExW.Call(sk, uintptr(unsafe.Pointer(u16("IsPromoted"))), 0, 0, ptr(&v), ptr(&n))
		pRegCloseKey.Call(sk)
		if r == 0 && v == 1 {
			return true
		}
	}
	return false
}

// setTrayPinned shows/hides our icon on the taskbar. Returns false when
// Windows has no per-icon switch (Windows 10, or icon not registered yet).
func setTrayPinned(on bool) bool {
	keys := ourTrayKeys()
	if len(keys) == 0 {
		return false
	}
	var v uint32
	if on {
		v = 1
	}
	for _, sub := range keys {
		var sk uintptr
		if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(sub))), 0, KEY_SET_VALUE, ptr(&sk)); r != 0 {
			continue
		}
		pRegSetValueExW.Call(sk, uintptr(unsafe.Pointer(u16("IsPromoted"))), 0, REG_DWORD, ptr(&v), 4)
		pRegCloseKey.Call(sk)
	}
	dbg("tray icon pinned=%v (%d keys)", on, len(keys))
	return true
}

func openTaskbarSettings() {
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16("ms-settings:taskbar"))), 0, 0, 1)
}

// toggleTrayPin is the Settings switch: flip it, or open Windows' taskbar
// settings where it can't be done for the user.
func toggleTrayPin() {
	if !setTrayPinned(!trayPinned()) {
		openTaskbarSettings()
	}
}

// autoPinOnce pins the icon the first time the app runs installed (Windows
// registers the icon a moment after it is added, so retry for a while).
func autoPinOnce() {
	if regGet("cfg|AutoPinned") == "1" {
		return
	}
	go func() {
		for i := 0; i < 12; i++ {
			time.Sleep(5 * time.Second)
			if setTrayPinned(true) {
				regSet("cfg|AutoPinned", "1")
				return
			}
		}
	}()
}
