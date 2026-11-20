//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// One-time move from the old "HyperX Battery" name to Headroom: settings,
// learned battery data, startup entry and install folder.

const (
	legacyKey   = `Software\HyperXBattery`
	legacyRun   = "HyperXBattery"
	legacyClass = "HyperXBatteryWnd"
)

var pRegEnumValueW = advapi32.NewProc("RegEnumValueW")

func legacyDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "HyperXBattery")
}

// closeLegacyInstance asks a running old version to exit.
func closeLegacyInstance() {
	if w, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(u16(legacyClass))), 0); w != 0 {
		pPostMessageW.Call(w, 0x0010 /*WM_CLOSE*/, 0, 0)
		time.Sleep(800 * time.Millisecond)
	}
}

type migration struct {
	done         bool // something was migrated this run
	hadInstall   bool // the old version was installed
	wantsStartup bool // the old version started with Windows
}

func migrateLegacy() migration {
	var m migration
	if regGet("Migrated") == "1" {
		return m
	}
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(legacyKey))), 0, KEY_READ, ptr(&k)); r != 0 {
		regSet("Migrated", "1") // nothing to migrate: fresh install
		return m
	}
	n := 0
	for i := uintptr(0); ; i++ {
		name := make([]uint16, 512)
		nl := uint32(len(name))
		data := make([]uint16, 512)
		dl := uint32(len(data) * 2)
		var typ uint32
		r, _, _ := pRegEnumValueW.Call(k, i, uintptr(unsafe.Pointer(&name[0])), ptr(&nl), 0, ptr(&typ), uintptr(unsafe.Pointer(&data[0])), ptr(&dl))
		if r != 0 {
			break
		}
		key := syscall.UTF16ToString(name[:nl])
		if typ != REG_SZ || key == "cfg|AutoPinned" { // the new exe needs pinning again
			continue
		}
		regSet(key, syscall.UTF16ToString(data[:dl/2]))
		n++
	}
	pRegCloseKey.Call(k)

	// startup entry: drop the old one, remember to add ours
	var rk uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(runKey))), 0, KEY_READ|KEY_SET_VALUE, ptr(&rk)); r == 0 {
		if q, _, _ := pRegQueryValueExW.Call(rk, uintptr(unsafe.Pointer(u16(legacyRun))), 0, 0, 0, 0); q == 0 {
			m.wantsStartup = true
			pRegDeleteValueW.Call(rk, uintptr(unsafe.Pointer(u16(legacyRun))))
		}
		pRegCloseKey.Call(rk)
	}
	if _, err := os.Stat(filepath.Join(legacyDir(), "HyperXBattery.exe")); err == nil {
		m.hadInstall = true
	}
	m.done = true
	regSet("Migrated", "1")
	dbg("migrated %d settings from HyperX Battery (installed=%v startup=%v)", n, m.hadInstall, m.wantsStartup)
	return m
}

// cleanupLegacy removes the old install folder once nothing uses it anymore.
func cleanupLegacy() {
	dir := legacyDir()
	if _, err := os.Stat(dir); err != nil {
		return
	}
	go func() {
		for i := 0; i < 12; i++ {
			time.Sleep(5 * time.Second)
			if err := os.RemoveAll(dir); err == nil {
				dbg("removed old folder %s", dir)
				return
			}
		}
	}()
}
