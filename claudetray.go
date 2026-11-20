//go:build windows

package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
	"unsafe"
)

// The orange Claude icon: a second tray icon with two rings, outer = 5-hour
// session left, inner = week left. Shown when Claude Code has reported usage.

const tmClaude = 20

var (
	claudeIconShown bool
	claudeIcon      uintptr
	claudeFileMod   time.Time
)

func claudeNID() NOTIFYICONDATAW {
	n := baseNID()
	n.UID = 2
	return n
}

func currentClaude() (View, bool) {
	for _, v := range views {
		if v.ID == "claude" {
			return v, true
		}
	}
	return claudeView(time.Now()) // panel row may be hidden; the icon still works
}

func drawClaudeIcon(v View, sz int, light bool) *canvas {
	c := newCanvas(sz, sz)
	f := float64(sz)
	cx := f / 2
	track := color{255, 255, 255, 0.28}
	if light {
		track = color{0, 0, 0, 0.20}
	}
	to := math.Max(2.2, f*0.15)
	ro := f/2 - to/2 - 0.4
	ti := math.Max(1.8, f*0.13)
	ri := ro - to/2 - ti/2 - math.Max(1, f*0.07)
	levels := []int{-1, -1}
	for i, p := range v.Parts {
		if i < 2 {
			levels[i] = p.Level
		}
	}
	c.ring(cx, cx, ro, to, 1, track)
	if levels[0] >= 0 {
		c.ring(cx, cx, ro, to, math.Max(0.04, float64(levels[0])/100), claudeTint(float64(levels[0]), false))
	}
	if ri > ti/2 {
		c.ring(cx, cx, ri, ti, 1, track)
		if levels[1] >= 0 {
			c.ring(cx, cx, ri, ti, math.Max(0.04, float64(levels[1])/100), claudeTint(float64(levels[1]), true))
		}
	}
	return c
}

func claudeTooltip(v View) string {
	s := "Claude"
	for _, p := range v.Parts {
		line := fmt.Sprintf("%s: %d%% left", strings.TrimSuffix(p.Label, " (5 h)"), p.Level)
		if p.Sub != "" {
			line += " · " + p.Sub
		}
		s += "\n" + line
	}
	return s
}

// renderClaudeIcon adds, updates or removes the orange icon.
func renderClaudeIcon() {
	v, ok := currentClaude()
	if !ok || !cfg.ClaudeTray {
		removeClaudeIcon()
		return
	}
	size, _, _ := pGetSystemMetrics.Call(SM_CXSMICON)
	sz := int(size)
	if sz <= 0 {
		sz = 16
	}
	ic := drawClaudeIcon(v, sz, lightTheme()).icon()
	n := claudeNID()
	n.UFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE
	n.UCallbackMessage = wmTray
	n.HIcon = ic
	copyU16(n.SzTip[:], claudeTooltip(v))
	op := uintptr(NIM_MODIFY)
	if !claudeIconShown {
		op = NIM_ADD
	}
	pShellNotifyIconW.Call(op, ptr(&n))
	if claudeIcon != 0 {
		pDestroyIcon.Call(claudeIcon)
	}
	claudeIcon = ic
	if !claudeIconShown {
		claudeIconShown = true
		pinClaudeOnce()
	}
}

func removeClaudeIcon() {
	if !claudeIconShown {
		return
	}
	n := claudeNID()
	pShellNotifyIconW.Call(NIM_DELETE, ptr(&n))
	claudeIconShown = false
}

// pinClaudeOnce shows the new icon on the taskbar the first time it appears.
func pinClaudeOnce() {
	if regGet("cfg|ClaudePinned") == "1" {
		return
	}
	go func() {
		for i := 0; i < 12; i++ {
			time.Sleep(5 * time.Second)
			if setTrayPinned(true) {
				regSet("cfg|ClaudePinned", "1")
				return
			}
		}
	}()
}

// checkClaudeFile re-renders as soon as Claude Code writes new numbers.
func checkClaudeFile() {
	st, err := os.Stat(claudeUsageFile())
	if err != nil {
		if claudeIconShown {
			renderClaudeIcon()
		}
		return
	}
	if st.ModTime().Equal(claudeFileMod) {
		return
	}
	claudeFileMod = st.ModTime()
	// rebuild the views list with fresh Claude numbers, without re-polling devices
	vs := views[:0:0]
	for _, v := range views {
		if v.ID != "claude" {
			vs = append(vs, v)
		}
	}
	if cv, ok := claudeView(time.Now()); ok && cfg.ClaudePanel {
		vs = append(vs, cv)
	}
	views = vs
	render()
}

// claudeSetupSnippet is the settings.json block for this install.
func claudeSetupSnippet() string {
	exe, _ := os.Executable()
	exe = strings.ReplaceAll(exe, `\`, "/")
	return "\"statusLine\": {\n  \"type\": \"command\",\n  \"command\": \"\\\"" + exe + "\\\" --statusline\",\n  \"padding\": 0\n}"
}

func copyClaudeSetup() {
	copyToClipboard(claudeSetupSnippet())
	showToast("Status line setup copied", "Paste it into ~/.claude/settings.json", 1, claudeOrange)
}

var _ = unsafe.Pointer(nil)
