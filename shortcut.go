package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
)

// Start menu shortcut, so Windows search ("headroom") finds the app. Windows
// search lists programs from Start menu shortcuts, not from exe files in
// folders, so an installed copy without one is invisible there. Checked on
// every start of the installed copy, so existing installs get it too.

func startMenuShortcut() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`, appName+".lnk")
}

func ensureStartMenuShortcut() {
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(filepath.Dir(exe), installDir()) {
		return // only the installed copy registers itself
	}
	if _, err := os.Stat(startMenuShortcut()); err == nil {
		return
	}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := createShortcut(startMenuShortcut(), exe); err != nil {
			dbg("start menu shortcut: %v", err)
		}
	}()
}

// createShortcut uses the Windows Script Host shell object, which ships with
// every Windows version, instead of hand-driving IShellLink.
func createShortcut(lnk, target string) error {
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		// already initialised on this thread is fine
		if oe, ok := err.(*ole.OleError); !ok || (oe.Code() != 1 && oe.Code() != 0x80010106) {
			return err
		}
	} else {
		defer ole.CoUninitialize()
	}
	clsid, err := ole.CLSIDFromProgID("WScript.Shell")
	if err != nil {
		return err
	}
	unk, err := ole.CreateInstance(clsid, ole.IID_IUnknown)
	if err != nil {
		return err
	}
	defer unk.Release()
	shell, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer shell.Release()
	v, err := shell.CallMethod("CreateShortcut", lnk)
	if err != nil {
		return err
	}
	sc := v.ToIDispatch()
	defer sc.Release()
	for _, p := range [][2]string{
		{"TargetPath", target},
		{"WorkingDirectory", filepath.Dir(target)},
		{"Description", "Headset battery and Claude usage in the tray"},
		{"IconLocation", target + ",0"},
	} {
		if _, err := sc.PutProperty(p[0], p[1]); err != nil {
			return err
		}
	}
	_, err = sc.CallMethod("Save")
	return err
}
