package main

import (
	"os"
	"path/filepath"
	rdebug "runtime/debug"
	"strings"
	"time"
)

// Crash capture. A windowsgui exe has no console, so a Go panic or fatal
// error would vanish without a trace. SetCrashOutput makes the runtime write
// the error and every goroutine's stack to crash.log as it dies. On the next
// start a non-empty crash.log is kept as crash-last.log, the user gets a
// small notice, and the troubleshooting report includes it.

var crashedLastTime bool

func crashDir() string { return filepath.Join(os.Getenv("LOCALAPPDATA"), "Headroom") }

func setupCrashLog() {
	dir := crashDir()
	os.MkdirAll(dir, 0o755)
	cur := filepath.Join(dir, "crash.log")
	if st, err := os.Stat(cur); err == nil && st.Size() > 0 {
		os.Remove(filepath.Join(dir, "crash-last.log"))
		if os.Rename(cur, filepath.Join(dir, "crash-last.log")) == nil {
			crashedLastTime = true
		}
	}
	f, err := os.OpenFile(cur, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	rdebug.SetCrashOutput(f, rdebug.CrashOptions{}) // the runtime keeps its own copy of f
	f.Close()
}

// lastCrashText is crash-last.log for the report (first 6000 characters,
// which holds the error and the goroutine that failed).
func lastCrashText() string {
	b, err := os.ReadFile(filepath.Join(crashDir(), "crash-last.log"))
	if err != nil || len(b) == 0 {
		return ""
	}
	s := string(b)
	if len(s) > 6000 {
		s = s[:6000] + "\n…"
	}
	st, _ := os.Stat(filepath.Join(crashDir(), "crash-last.log"))
	when := ""
	if st != nil {
		when = " (found " + st.ModTime().Format("2006-01-02 15:04") + ")"
	}
	return "Last crash" + when + ":\r\n" + strings.ReplaceAll(s, "\n", "\r\n")
}

func noticeLastCrash() {
	if !crashedLastTime {
		return
	}
	go func() {
		time.Sleep(3 * time.Second)
		pPostMessageW.Call(hwnd, wmCrashNotice, 0, 0)
	}()
}
