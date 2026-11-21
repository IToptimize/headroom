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

// Claude limit colours ("Dusk"): each limit has its own calm colour while
// there's room, and all of them share the same warning colours as they
// fill: yellow at 60 %, orange 80 %, red 92 %, full red when used up.
const (
	limSession = 0 // 5-hour
	limWeek    = 1 // weekly, all models
	limModel   = 2 // a model's own weekly limit (e.g. Fable)
)

var limitCalm = [3]color{
	{139, 112, 224, 1}, // violet
	{91, 141, 239, 1},  // blue
	{63, 181, 168, 1},  // teal
}

func usedColor(used float64, light bool) color { return limitColor(limSession, used) }

func limitColor(kind int, used float64) color {
	if kind < 0 || kind > 2 {
		kind = limSession
	}
	yellow, orange, red := color{226, 190, 76, 1}, color{232, 136, 64, 1}, color{226, 76, 76, 1}
	mix := func(a, b color, t float64) color {
		m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
		return color{m(a.r, b.r), m(a.g, b.g), m(a.b, b.b), 1}
	}
	// no blend from the calm colour into yellow: mixing blue or teal with
	// yellow passes through grey (RGB) or green (hue), both misleading. A
	// clean switch at 60 % reads as "getting tight now".
	switch {
	case used >= 99.5:
		return colRed
	case used < 60:
		return limitCalm[kind]
	case used < 80:
		return mix(yellow, orange, (used-60)/20)
	case used < 92:
		return mix(orange, red, (used-80)/12)
	}
	return red
}

// drawClaudeIcon: outer ring = 5-hour session, inner = week.
// Headroom look: ring fills with what's left. Claude look: with what's used.
// Both colour by usage: purple -> yellow -> orange -> red.
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
	// outer = 5-hour session; inner = the weekly limit chosen in settings:
	// all models, the model-specific one (e.g. Fable), or whichever is
	// closer to running out
	levels := []int{-1, -1}
	week, model := -1, -1
	for _, p := range v.Parts {
		switch {
		case strings.HasPrefix(p.Label, "Session"):
			levels[0] = p.Level
		case p.Label == "Week":
			week = p.Level
		case strings.HasPrefix(p.Label, "Week · ") && model < 0:
			model = p.Level
		}
	}
	levels[1] = week
	innerKind := limWeek
	switch {
	case model < 0:
	case cfg.ClaudeInner == 1, cfg.ClaudeInner == 2 && (week < 0 || model < week):
		levels[1], innerKind = model, limModel
	}
	draw := func(r, t float64, left int, kind int) {
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
				c.ring(cx, cx, r, t, math.Max(0.04, used/100), limitColor(kind, used))
			}
			return
		}
		c.ring(cx, cx, r, t, math.Max(0.04, float64(left)/100), limitColor(kind, 100-float64(left)))
	}
	draw(ro, to, levels[0], limSession)
	if ri > ti/2 {
		draw(ri, ti, levels[1], innerKind)
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
	render() // fresh Claude numbers for the orange icon and its panel
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
		forcePlanCheck.Store(true)
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
