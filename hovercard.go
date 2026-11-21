//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unsafe"
)

// The hover card for the orange icon: one block per limit with % used,
// a bar in the usage colour, and when it resets.

const WS_EX_TRANSPARENT = 0x00000020

var (
	hoverHwnd    uintptr
	hoverVisible bool
)

func initHoverCard() {
	registerPopupClass("HXHover", func(h, msg, wp, lp uintptr) uintptr {
		r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
		return r
	})
	hoverHwnd = createPopup("HXHover", WS_EX_NOACTIVATE|WS_EX_TRANSPARENT)
}

func planName(p string) string {
	switch strings.ToLower(p) {
	case "pro":
		return "Pro"
	case "max":
		return "Max"
	case "team":
		return "Team"
	case "enterprise":
		return "Enterprise"
	}
	return ""
}

func renderHoverCard(s float64) *canvas {
	th := currentTheme()
	light := panelLight()
	u, _ := readClaudeUsage()
	now := time.Now()
	type block struct {
		name string
		has  bool
		used float64
		ends int64
	}
	blocks := []block{
		{"5-hour session", u.HasSession, u.SessionUsed, u.SessionEnds},
		{"Weekly", u.HasWeek, u.WeekUsed, u.WeekEnds},
	}
	for _, e := range u.Extra {
		blocks = append(blocks, block{"Weekly · " + e.Name, true, e.Used, e.Ends})
	}
	n := 0
	for _, b := range blocks {
		if b.has {
			n++
		}
	}
	W, H := 300*s, (46+float64(n)*58+30)*s
	c, ox, oy := card(W, H, s, th)
	lx, rx := ox+16*s, ox+W-16*s

	// header: small orange dot, "Claude usage", plan as a soft pill on the right
	c.fillCircle(lx+4*s, oy+22*s, 4*s, claudeOrange)
	c.text("Claude usage", lx+14*s, oy+14*s, int(13*s), 600, th.fg, alignLeft)
	if p := planName(u.Plan); p != "" {
		pw := float64(renderText(p, int(11*s), 600, 0).w)
		c.fillRoundRect(rx-pw-14*s, oy+12*s, pw+14*s, 20*s, 10*s, color{claudeOrange.r, claudeOrange.g, claudeOrange.b, 0.16})
		c.text(p, rx-7*s, oy+15*s, int(11*s), 600, claudeOrange, alignRight)
	}

	y := oy + 46*s
	for _, b := range blocks {
		if !b.has {
			continue
		}
		used := math.Min(100, math.Max(0, b.used))
		resetPassed := b.ends > 0 && now.Unix() >= b.ends
		if resetPassed {
			used = 0
		}
		col := usedColor(used, light)
		c.text(b.name, lx, y, int(12*s), 600, th.fg, alignLeft)
		pcol := th.fg
		if used >= 99.5 {
			pcol = colRed
		}
		c.text(fmt.Sprintf("%.0f%% used", used), rx, y, int(12*s), 600, pcol, alignRight)
		by := y + 22*s
		bh := 6 * s
		c.fillRoundRect(lx, by, rx-lx, bh, bh/2, th.track)
		if used > 0 {
			c.fillRoundRect(lx, by, math.Max(bh, (rx-lx)*used/100), bh, bh/2, col)
		}
		sub, sc := "", th.muted
		switch {
		case resetPassed:
			sub = "Reset — fresh limit"
		case used >= 99.5 && b.ends > 0:
			sub, sc = "Ready in "+untilText(b.ends, now)+" · "+time.Unix(b.ends, 0).Format("15:04"), colRed
		case b.ends > 0 && b.ends-now.Unix() < 20*3600:
			sub = "Resets " + time.Unix(b.ends, 0).Format("15:04") + " · in " + untilText(b.ends, now)
		case b.ends > 0:
			sub = "Resets " + time.Unix(b.ends, 0).Format("Mon 15:04")
		}
		if sub != "" {
			c.text(sub, lx, by+11*s, int(11*s), 400, sc, alignLeft)
		}
		y += 58 * s
	}
	c.fillRoundRect(lx, oy+H-30*s, rx-lx, math.Max(1, s), 0, th.border)
	c.text("Updated "+time.Unix(u.Updated, 0).Format("15:04"), lx, oy+H-23*s, int(11*s), 400, th.muted, alignLeft)
	c.text("Click for details", rx, oy+H-23*s, int(11*s), 400, th.muted, alignRight)
	return c
}

// Hover timing: the card only appears once the mouse has rested on the
// icon for half a second, so passing over the tray doesn't flash it.
const (
	tmHover    = 21
	hoverDelay = 500
)

var hoverPending bool

func hoverEnter() {
	if !cfg.ClaudeHover || hoverVisible {
		return
	}
	hoverPending = true
	pSetTimer.Call(hwnd, tmHover, hoverDelay, 0)
}

func hoverLeave() {
	hoverPending = false
	pKillTimer.Call(hwnd, tmHover)
	hideHoverCard()
}

func hoverTimer() {
	pKillTimer.Call(hwnd, tmHover)
	if !hoverPending {
		return
	}
	hoverPending = false
	var pt POINT
	pGetCursorPos.Call(ptr(&pt))
	if r, ok := claudeIconRect(); ok {
		// still resting on the icon? (a few px of slack)
		if pt.X < r.Left-4 || pt.X > r.Right+4 || pt.Y < r.Top-4 || pt.Y > r.Bottom+4 {
			return
		}
		showHoverCard(r)
		return
	}
	showHoverCard(RECT{pt.X - 8, pt.Y - 8, pt.X + 8, pt.Y + 8})
}

type notifyIconIdentifier struct {
	CbSize uint32
	HWnd   uintptr
	UID    uint32
	Guid   GUID
}

var pShellNotifyIconGetRect = shell32.NewProc("Shell_NotifyIconGetRect")

// claudeIconRect: where the orange icon sits on screen (taskbar or the
// hidden-icons overflow), in physical pixels.
func claudeIconRect() (RECT, bool) {
	if pShellNotifyIconGetRect.Find() != nil {
		return RECT{}, false
	}
	id := notifyIconIdentifier{HWnd: hwnd, UID: 2}
	id.CbSize = uint32(unsafe.Sizeof(id))
	var r RECT
	hr, _, _ := pShellNotifyIconGetRect.Call(ptr(&id), ptr(&r))
	if hr != 0 || r.Right <= r.Left {
		return RECT{}, false
	}
	return r, true
}

// showHoverCard places the card just above the icon (or beside / below it
// when the taskbar is on another edge), centred on it.
func showHoverCard(icon RECT) {
	if !cfg.ClaudeHover || flyVisible || setVisible {
		return
	}
	if _, ok := readClaudeUsage(); !ok {
		return
	}
	cx, cy := (icon.Left+icon.Right)/2, (icon.Top+icon.Bottom)/2
	work, s := monitorAt(POINT{cx, cy}, false)
	c := renderHoverCard(s)
	pad := int(shadowPad * s)
	gap := int(8 * s)
	w, h := c.w-2*pad, c.h-2*pad
	px, py := int(cx)-w/2, int(icon.Top)-h-gap // default: above
	switch {
	case icon.Bottom <= work.Top+2: // taskbar at the top
		py = int(icon.Bottom) + gap
	case icon.Right <= work.Left+2: // taskbar on the left
		px, py = int(icon.Right)+gap, int(cy)-h/2
	case icon.Left >= work.Right-2: // taskbar on the right
		px, py = int(icon.Left)-w-gap, int(cy)-h/2
	}
	m := int(6 * s)
	px = max(int(work.Left)+m, min(px, int(work.Right)-w-m))
	if py+h > int(work.Bottom) && icon.Top >= work.Bottom-2 {
		py = int(work.Bottom) - h - gap
	}
	py = max(int(work.Top)+m, py)
	c.present(hoverHwnd, px-pad, py-pad, 255)
	pShowWindow.Call(hoverHwnd, SW_SHOWNOACTIVATE)
	hoverVisible = true
}

func hideHoverCard() {
	if hoverVisible {
		pShowWindow.Call(hoverHwnd, SW_HIDE)
		hoverVisible = false
	}
}
