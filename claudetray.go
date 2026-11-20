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

// usedColor: Claude's own scheme for "how much is used".
func usedColor(used float64, light bool) color {
	switch {
	case used >= 90:
		return colRed
	case used >= 75:
		return colAmber
	}
	if light {
		return color{40, 40, 40, 1}
	}
	return color{235, 235, 235, 1}
}

// drawClaudeIcon: outer ring = 5-hour session, inner = week.
// Headroom look: ring fills with what's left, Claude orange/peach.
// Claude look: ring fills with what's used, neutral -> amber -> red.
// A used-up window is a full red ring in both looks.
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
	draw := func(r, t float64, left int, week bool) {
		c.ring(cx, cx, r, t, 1, track)
		if left < 0 {
			return
		}
		if left <= 0 {
			c.ring(cx, cx, r, t, 1, colRed) // used up
			return
		}
		if cfg.ClaudeLook == 1 {
			used := 100 - float64(left)
			if used > 0 {
				c.ring(cx, cx, r, t, math.Max(0.04, used/100), usedColor(used, light))
			}
			return
		}
		c.ring(cx, cx, r, t, math.Max(0.04, float64(left)/100), claudeTint(float64(left), week))
	}
	draw(ro, to, levels[0], false)
	if ri > ti/2 {
		draw(ri, ti, levels[1], true)
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
	if !cfg.ClaudeHover {
		n.UFlags |= NIF_SHOWTIP // plain tooltip instead of our hover card
	}
	n.UCallbackMessage = wmTray
	n.HIcon = ic
	copyU16(n.SzTip[:], claudeTooltip(v))
	op := uintptr(NIM_MODIFY)
	if !claudeIconShown {
		op = NIM_ADD
	}
	pShellNotifyIconW.Call(op, ptr(&n))
	if op == NIM_ADD {
		// version 4: hover in/out events (NIN_POPUPOPEN/CLOSE) for the card
		n.UVersion = 4
		pShellNotifyIconW.Call(NIM_SETVERSION, ptr(&n))
	}
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
	removeClaudeIconForce()
}

// removeClaudeIconForce deletes the icon so it can be re-added with new flags.
func removeClaudeIconForce() {
	n := claudeNID()
	pShellNotifyIconW.Call(NIM_DELETE, ptr(&n))
	claudeIconShown = false
	hideHoverCard()
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
	checkClaudeAlerts()
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
	cmd := exe
	if strings.Contains(exe, " ") { // only quote when needed: PowerShell treats a quoted path as text
		cmd = `\"` + exe + `\"`
	}
	return "\"statusLine\": {\n  \"type\": \"command\",\n  \"command\": \"" + cmd + " --statusline\",\n  \"padding\": 0\n}"
}

func copyClaudeSetup() {
	copyToClipboard(claudeSetupSnippet())
	showToast("Status line setup copied", "Paste it into ~/.claude/settings.json", 1, claudeOrange)
}

var _ = unsafe.Pointer(nil)

// showClaudeMenu is the right-click menu of the orange icon.
func showClaudeMenu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(flags uintptr, id uintptr, text string) {
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(u16(text))))
	}
	if v, ok := currentClaude(); ok {
		for _, p := range v.Parts {
			line := fmt.Sprintf("%s: %d%% left", strings.TrimSuffix(p.Label, " (5 h)"), p.Level)
			if p.Sub != "" {
				line += " · " + p.Sub
			}
			add(MF_STRING|MF_GRAYED, 0, line)
		}
		add(MF_SEPARATOR, 0, "")
	}
	const (
		idCRefresh = 201
		idCSet     = 202
		idCHide    = 203
	)
	if cfg.ClaudeFetch {
		add(MF_STRING, idCRefresh, "Refresh now")
	}
	add(MF_STRING, idCSet, "Claude settings…")
	add(MF_STRING, idCHide, "Hide this icon")
	add(MF_SEPARATOR, 0, "")
	add(MF_STRING, idExit, "Exit Headroom")
	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	pSetForegroundWindow.Call(hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, TPM_RIGHTBUTTON|TPM_RETURNCMD|TPM_NONOTIFY, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	switch cmd {
	case idCRefresh:
		kickClaudeFetch()
	case idCSet:
		showSettings(1)
	case idCHide:
		cfg.ClaudeTray = false
		saveConfig()
		removeClaudeIcon()
	case idExit:
		pDestroyWindow.Call(hwnd)
	}
}

// ---- reminders when the 5-hour limit runs out and when it's back ----

var (
	alertInit      bool
	alertExhausted bool  // last seen: session used up
	alertEnds      int64 // reset time of that used-up window
	alertDoneFor   int64 // reset already announced for this time
)

func checkClaudeAlerts() {
	u, ok := readClaudeUsage()
	if !ok || !u.HasSession {
		return
	}
	now := time.Now()
	exhausted := u.SessionUsed >= 99.5 && u.SessionEnds > now.Unix()
	if !alertInit { // no pop-ups for what was already true at start-up
		alertInit, alertExhausted, alertEnds = true, exhausted, u.SessionEnds
		return
	}
	if exhausted && !alertExhausted && cfg.ClaudeAlerts {
		at := time.Unix(u.SessionEnds, 0)
		showToast("Claude 5-hour limit used up", "Ready again at "+at.Format("15:04")+" · in "+untilText(u.SessionEnds, now), 1, colRed)
	}
	if exhausted {
		alertEnds = u.SessionEnds
	}
	// the reset moment of a window we saw used up
	if alertExhausted && alertEnds > 0 && now.Unix() >= alertEnds && alertDoneFor != alertEnds {
		alertDoneFor = alertEnds
		if cfg.ClaudeAlerts {
			showToast("Claude is ready again", "Your 5-hour limit has reset", 1, claudeOrange)
		}
		render() // the ring goes back to full
	}
	alertExhausted = exhausted
}
