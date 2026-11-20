//go:build windows

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The hover card for the orange icon, modelled on Claude's own usage popup:
// one block per limit with name, reset time, % used and a coloured bar.

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
	u, _ := readClaudeUsage()
	now := time.Now()
	type block struct {
		name string
		has  bool
		used float64
		ends int64
	}
	blocks := []block{
		{"5-hour limit", u.HasSession, u.SessionUsed, u.SessionEnds},
		{"Weekly · All models", u.HasWeek, u.WeekUsed, u.WeekEnds},
	}
	n := 0
	for _, b := range blocks {
		if b.has {
			n++
		}
	}
	W, H := 340*s, (52+float64(n)*62+14)*s
	c, ox, oy := card(W, H, s, th)
	lx, rx := ox+18*s, ox+W-18*s

	title := "Your usage limits"
	tw := c.text(title, lx, oy+16*s, int(13*s), 400, th.muted, alignLeft)
	if p := planName(u.Plan); p != "" {
		c.text("· "+p, lx+float64(tw)+5*s, oy+16*s, int(13*s), 600, th.fg, alignLeft)
	}

	y := oy + 52*s
	for _, b := range blocks {
		if !b.has {
			continue
		}
		used := math.Min(100, math.Max(0, b.used))
		resetPassed := b.ends > 0 && now.Unix() >= b.ends
		if resetPassed {
			used = 0
		}
		col := claudeOrange // like Claude: amber when high, red when nearly out
		if used >= 75 {
			col = usedColor(used, panelLight())
		}
		c.text(b.name, lx, y, int(13*s), 600, th.fg, alignLeft)
		pct := fmt.Sprintf("%.0f%%", used)
		pcol := th.fg
		if used >= 90 {
			pcol = colRed
		}
		pw := c.text(pct, rx, y, int(13*s), 600, pcol, alignRight)
		right := ""
		switch {
		case resetPassed:
			right = "Reset"
		case used >= 99.5 && b.ends > 0:
			right = "Ready in " + untilText(b.ends, now)
		case b.ends > 0:
			right = "Resets " + time.Unix(b.ends, 0).Format("Mon 15:04")
		}
		if right != "" {
			rc := th.muted
			if strings.HasPrefix(right, "Ready") {
				rc = colRed
			}
			c.text(right, rx-float64(pw)-14*s, y+1*s, int(12*s), 400, rc, alignRight)
		}
		by := y + 26*s
		bh := 5 * s
		c.fillRoundRect(lx, by, rx-lx, bh, bh/2, th.track)
		if used > 0 {
			c.fillRoundRect(lx, by, math.Max(bh, (rx-lx)*used/100), bh, bh/2, col)
		}
		y += 62 * s
	}
	foot := "Updated " + time.Unix(u.Updated, 0).Format("15:04") + " · click for details"
	c.text(foot, lx, oy+H-28*s, int(11*s), 400, th.muted, alignLeft)
	return c
}

// showHoverCard places the card above (or beside) the icon at screen point x,y.
func showHoverCard(x, y int) {
	if !cfg.ClaudeHover || flyVisible || setVisible {
		return
	}
	if _, ok := readClaudeUsage(); !ok {
		return
	}
	pt := POINT{int32(x), int32(y)}
	work, s := monitorAt(pt, false)
	c := renderHoverCard(s)
	pad := int(shadowPad * s)
	m := int(10 * s)
	w, h := c.w-2*pad, c.h-2*pad
	px := x - w/2
	py := int(work.Bottom) - h - m
	switch {
	case pt.Y < work.Top:
		py = int(work.Top) + m
	case pt.Y >= work.Top && pt.Y < work.Bottom:
		py = y - h/2
		if pt.X >= work.Right {
			px = int(work.Right) - w - m
		} else if pt.X < work.Left {
			px = int(work.Left) + m
		}
	}
	px = max(int(work.Left)+m, min(px, int(work.Right)-w-m))
	py = max(int(work.Top)+m, min(py, int(work.Bottom)-h-m))
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
