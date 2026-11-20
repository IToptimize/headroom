//go:build windows

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	settingsKey      = `Software\Headroom`
	MB_YESNO         = 0x04
	MB_ICONQUESTION  = 0x20
	MB_ICONERROR     = 0x10
	MB_SETFOREGROUND = 0x10000
	IDYES            = 6
	KEY_ALL_ACCESS   = 0xF003F
)

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Headroom")
}

func msgBox(text string, flags uintptr) uintptr {
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(appName))), flags|MB_SETFOREGROUND)
	return r
}

func setupDone() bool {
	var k uintptr
	if r, _, _ := pRegOpenKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(settingsKey))), 0, KEY_READ, ptr(&k)); r != 0 {
		return false
	}
	defer pRegCloseKey.Call(k)
	r, _, _ := pRegQueryValueExW.Call(k, uintptr(unsafe.Pointer(u16("SetupDone"))), 0, 0, 0, 0)
	return r == 0
}

func markSetupDone() {
	var k uintptr
	if r, _, _ := pRegCreateKeyExW.Call(HKEY_CURRENT_USER, uintptr(unsafe.Pointer(u16(settingsKey))), 0, 0, 0, KEY_ALL_ACCESS, 0, ptr(&k), 0); r != 0 {
		return
	}
	defer pRegCloseKey.Call(k)
	v := syscall.StringToUTF16("1")
	pRegSetValueExW.Call(k, uintptr(unsafe.Pointer(u16("SetupDone"))), 0, REG_SZ, uintptr(unsafe.Pointer(&v[0])), uintptr(len(v)*2))
}

// firstRunSetup asks once whether to install and start with Windows.
// It returns true when this process should exit because the installed copy was launched.
func firstRunSetup(mutex uintptr, mig migration) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	dest := filepath.Join(installDir(), "Headroom.exe")
	runningInstalled := strings.EqualFold(filepath.Clean(exe), filepath.Clean(dest))

	if setupDone() {
		// an installed copy exists: update it with this exe, then run it
		if !runningInstalled {
			// installed copy exists, or the old HyperX Battery was installed: (re)install here
			if _, err := os.Stat(dest); err == nil || mig.hadInstall {
				// the old copy may still be exiting and holding the file: retry briefly
				var err error
				for i := 0; i < 20; i++ {
					if err = copyFile(exe, dest); err == nil {
						break
					}
					time.Sleep(250 * time.Millisecond)
				}
				if err != nil {
					dbg("update installed copy: %v", err)
				}
				if mig.wantsStartup {
					setStartupPath(dest)
				}
				syscall.CloseHandle(syscall.Handle(mutex))
				if exec.Command(dest, os.Args[1:]...).Start() == nil {
					return true
				}
			}
		}
		// keep the startup entry pointing at wherever we actually run from
		if startupEnabled() || mig.wantsStartup {
			setStartup(true)
		}
		return false
	}
	markSetupDone()

	if msgBox("Start Headroom automatically when Windows starts?\n\n"+
		"Yes installs it to your user folder and adds it to startup (no admin needed).\n"+
		"You can change this any time: right-click the tray icon > Start with Windows.",
		MB_YESNO|MB_ICONQUESTION) != IDYES {
		return false
	}
	if runningInstalled {
		setStartup(true)
		return false
	}
	if err := copyFile(exe, dest); err != nil {
		msgBox("Couldn't install to "+installDir()+":\n"+err.Error()+"\n\nRunning from the current location instead.", MB_ICONERROR)
		setStartup(true)
		return false
	}
	setStartupPath(dest)
	syscall.CloseHandle(syscall.Handle(mutex)) // let the installed copy take the single-instance lock
	if err := exec.Command(dest, os.Args[1:]...).Start(); err != nil {
		return false
	}
	return true
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
