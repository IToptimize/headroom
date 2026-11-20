//go:build windows

package main

import (
	"math"
	"time"
)

// Clickable areas recorded while rendering a panel (canvas pixel coords).
type hit struct {
	x0, y0, x1, y1 float64
	fn             func()
}

var (
	flyHits, setHits []hit
	setHwnd          uintptr
	setVisible       bool
	setHidden        time.Time
	setX, setY       int
	setScale         float64
)

func clickHits(hs []hit, lp uintptr) {
	x := float64(int16(lp & 0xFFFF))
	y := float64(int16((lp >> 16) & 0xFFFF))
	for _, h := range hs {
		if x >= h.x0 && x <= h.x1 && y >= h.y0 && y <= h.y1 {
			h.fn()
			return
		}
	}
}

func accentText(th theme) color {
	if cfg.ColorMode == 1 {
		return accentColor()
	}
	return colBlue
}

// segmented draws a right-aligned pill group; returns its left edge.
func segmented(c *canvas, right, y float64, opts []string, sel int, s float64, th theme, pick func(int)) float64 {
	pad, h := 10*s, 26*s
	widths := make([]float64, len(opts))
	total := 4 * s
	for i, o := range opts {
		widths[i] = float64(renderText(o, int(12*s), 600, 0).w) + 2*pad
		total += widths[i]
	}
	left := right - total
	c.fillRoundRect(left, y, total, h, h/2, th.track)
	x := left + 2*s
	for i, o := range opts {
		w := widths[i]
		fg := th.fg
		if i == sel {
			c.fillRoundRect(x, y+2*s, w, h-4*s, (h-4*s)/2, accentText(th))
			fg = color{255, 255, 255, 1}
		}
		c.text(o, x+w/2, y+5*s, int(12*s), 600, fg, alignCenter)
		i := i
		setHits = append(setHits, hit{x, y, x + w, y + h, func() { pick(i) }})
		x += w
	}
	return left
}

// chips draws independently toggled pills, right-aligned.
func chips(c *canvas, right, y float64, opts []string, on []bool, s float64, th theme, toggle func(int)) {
	pad, h, gap := 10*s, 26*s, 6*s
	x := right
	for i := len(opts) - 1; i >= 0; i-- {
		w := float64(renderText(opts[i], int(12*s), 600, 0).w) + 2*pad
		x -= w
		fg := th.fg
		if on[i] {
			c.fillRoundRect(x, y, w, h, h/2, accentText(th))
			fg = color{255, 255, 255, 1}
		} else {
			c.strokeRoundRect(x, y, w, h, h/2, math.Max(1, s), th.muted.alpha(0.6))
		}
		c.text(opts[i], x+w/2, y+5*s, int(12*s), 600, fg, alignCenter)
		i, x0 := i, x
		setHits = append(setHits, hit{x0, y, x0 + w, y + h, func() { toggle(i) }})
		x -= gap
	}
}

func toggleSwitch(c *canvas, right, y float64, on bool, s float64, th theme, flip func()) {
	w, h := 40*s, 22*s
	x := right - w
	if on {
		c.fillRoundRect(x, y, w, h, h/2, accentText(th))
		c.fillCircle(x+w-h/2, y+h/2, h/2-4*s, color{255, 255, 255, 1})
	} else {
		c.strokeRoundRect(x, y, w, h, h/2, math.Max(1, s), th.muted)
		c.fillCircle(x+h/2, y+h/2, h/2-5*s, th.muted)
	}
	setHits = append(setHits, hit{x, y, x + w, y + h, flip})
}

var setTab int // 0 Headroom, 1 Claude

func renderSettings(s float64) *canvas {
	th := currentTheme()
	W, H := 760*s, 430*s
	c, ox, oy := card(W, H, s, th)
	setHits = nil
	col1R, col2L := ox+W/2-18*s, ox+W/2+20*s
	right := col1R
	lx := ox + 20*s

	c.text("Settings", lx, oy+16*s, int(17*s), 600, th.fg, alignLeft)
	// tabs: which part of the app you're changing
	segmented(c, ox+W/2+90*s, oy+14*s, []string{"Headroom", "Claude"}, setTab, s, th, func(i int) {
		setTab = i
		applySettings()
	})
	// close (×) drawn as two thin rounded bars
	cx, cy := ox+W-26*s, oy+28*s
	for _, d := range []float64{1, -1} {
		for t := -6.0; t <= 6; t += 0.5 {
			c.fillCircle(cx+t*s, cy+d*t*s, 0.9*s, th.muted)
		}
	}
	setHits = append(setHits, hit{cx - 14*s, cy - 14*s, cx + 14*s, cy + 14*s, hideSettings})

	top := oy + 66*s
	y := top
	section := func(t string) {
		c.text(t, lx, y, int(11*s), 600, th.muted, alignLeft)
		y += 24 * s
	}
	row := func(label string, draw func(y float64)) {
		c.text(label, lx, y+5*s, int(13*s), 400, th.fg, alignLeft)
		draw(y)
		y += 40 * s
	}
	rowSub := func(label, sub string, draw func(y float64)) {
		c.text(label, lx, y+1*s, int(13*s), 400, th.fg, alignLeft)
		c.text(sub, lx, y+20*s, int(11*s), 400, th.muted, alignLeft)
		draw(y)
		y += 44 * s
	}
	changed := func() {
		saveConfig()
		applySettings()
	}
	secondColumn := func() {
		c.fillRoundRect(ox+W/2, top, math.Max(1, s), H-(top-oy)-48*s, 0, th.border)
		lx, right, y = col2L, ox+W-20*s, top
	}

	if setTab == 0 {
		section("APPEARANCE")
		row("Theme", func(y float64) {
			segmented(c, right, y, []string{"System", "Light", "Dark"}, cfg.Theme, s, th, func(i int) { cfg.Theme = i; changed() })
		})
		row("Colours", func(y float64) {
			segmented(c, right, y, []string{"Battery", "Accent"}, cfg.ColorMode, s, th, func(i int) { cfg.ColorMode = i; changed() })
		})
		row("Accent colour", func(y float64) {
			r := 9 * s
			x := right - r
			for i := len(accents) - 1; i >= 0; i-- {
				a := accents[i]
				if cfg.ColorMode != 1 {
					a = a.alpha(0.45)
				}
				c.fillCircle(x, y+13*s, r, a)
				if i == cfg.Accent {
					c.ring(x, y+13*s, r+3.5*s, 1.5*s, 1, th.fg.alpha(0.8))
				}
				i, x0 := i, x
				setHits = append(setHits, hit{x0 - r - 3*s, y, x0 + r + 3*s, y + 26*s, func() { cfg.Accent = i; cfg.ColorMode = 1; changed() }})
				x -= 2*r + 10*s
			}
		})
		row("Tray icon", func(y float64) {
			segmented(c, right, y, []string{"Ring", "Ring + %", "Pie"}, cfg.Icon, s, th, func(i int) { cfg.Icon = i; changed() })
		})
		sel := 0
		if cfg.Compact {
			sel = 1
		}
		row("Layout", func(y float64) {
			segmented(c, right, y, []string{"Comfortable", "Compact"}, sel, s, th, func(i int) { cfg.Compact = i == 1; changed() })
		})
		y += 6 * s
		section("NOTIFICATIONS")
		row("Pop-up at", func(y float64) {
			labels := make([]string, len(popupLevels))
			on := make([]bool, len(popupLevels))
			for i, l := range popupLevels {
				labels[i] = itoa(l) + "%"
				on[i] = cfg.Popups[l]
			}
			chips(c, right, y, labels, on, s, th, func(i int) {
				l := popupLevels[i]
				cfg.Popups[l] = !cfg.Popups[l]
				changed()
			})
		})

		secondColumn()
		section("GENERAL")
		row("Refresh every", func(y float64) {
			opts := []int{30, 60, 120}
			sel := 1
			for i, o := range opts {
				if cfg.Interval == o {
					sel = i
				}
			}
			segmented(c, right, y, []string{"30 s", "1 min", "2 min"}, sel, s, th, func(i int) { cfg.Interval = opts[i]; changed(); requestPoll() })
		})
		row("Start with Windows", func(y float64) {
			toggleSwitch(c, right, y+2*s, startupEnabled(), s, th, func() { setStartup(!startupEnabled()); applySettings() })
		})
		row("Always show in taskbar", func(y float64) {
			toggleSwitch(c, right, y+2*s, trayPinned(), s, th, func() { toggleTrayPin(); applySettings() })
		})
		y += 6 * s
		section("HELP")
		row("Headset not working?", func(y float64) {
			segmented(c, right, y, []string{"Create report"}, 0, s, th, func(int) { hideSettings(); createReport() })
		})
	} else {
		section("TRAY & PANEL")
		row("Orange icon in the tray", func(y float64) {
			toggleSwitch(c, right, y+2*s, cfg.ClaudeTray, s, th, func() { cfg.ClaudeTray = !cfg.ClaudeTray; changed() })
		})
		row("Look", func(y float64) {
			segmented(c, right, y, []string{"Headroom", "Claude"}, cfg.ClaudeLook, s, th, func(i int) { cfg.ClaudeLook = i; changed() })
		})
		row("Details when hovering the icon", func(y float64) {
			toggleSwitch(c, right, y+2*s, cfg.ClaudeHover, s, th, func() {
				cfg.ClaudeHover = !cfg.ClaudeHover
				changed()
				claudeIconShown = false
				removeClaudeIconForce()
				renderClaudeIcon()
			})
		})
		row("Show in the headset panel", func(y float64) {
			toggleSwitch(c, right, y+2*s, cfg.ClaudePanel, s, th, func() { cfg.ClaudePanel = !cfg.ClaudePanel; changed(); claudeFileMod = time.Time{}; checkClaudeFile() })
		})
		rowSub("Pop-up when the 5-hour limit", "runs out, and when it's ready again", func(y float64) {
			toggleSwitch(c, right, y+2*s, cfg.ClaudeAlerts, s, th, func() { cfg.ClaudeAlerts = !cfg.ClaudeAlerts; changed() })
		})

		secondColumn()
		section("WHERE THE NUMBERS COME FROM")
		sub := "Unofficial · token stays on this PC · works with the VS Code panel"
		if cfg.ClaudeFetch {
			if st := getFetchStatus(); st != "" {
				sub = st
			} else {
				sub = "Checking…"
			}
		}
		rowSub("Read usage from my Claude login", sub, func(y float64) {
			toggleSwitch(c, right, y+2*s, cfg.ClaudeFetch, s, th, func() {
				cfg.ClaudeFetch = !cfg.ClaudeFetch
				changed()
				if cfg.ClaudeFetch {
					setFetchStatus("")
					kickClaudeFetch()
				}
			})
		})
		if cfg.ClaudeFetch {
			row("Check every", func(y float64) {
				opts := []int{2, 3, 5}
				sel := 1
				for i, o := range opts {
					if cfg.ClaudeEvery == o {
						sel = i
					}
				}
				segmented(c, right, y, []string{"2 min", "3 min", "5 min"}, sel, s, th, func(i int) {
					cfg.ClaudeEvery = opts[i] // takes effect from the next check
					changed()
				})
			})
		}
		rowSub("Status line for Claude Code", "Live rings under the prompt in a terminal", func(y float64) {
			segmented(c, right, y, []string{"Copy setup"}, 0, s, th, func(int) { copyClaudeSetup() })
		})
	}

	c.text(appName+" "+appVersion, ox+20*s, oy+H-28*s, int(11*s), 400, th.muted, alignLeft)
	return c
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// applySettings re-renders everything after a change.
func applySettings() {
	render()
	if setVisible {
		renderSettings(setScale).present(setHwnd, setX, setY, 255)
	}
}

func showSettings(tab int) {
	setTab = tab
	if setVisible {
		applySettings()
		return
	}
	c, x, y, s := placeNearTray(renderSettings)
	setX, setY, setScale = x, y, s
	c.present(setHwnd, x, y, 255)
	pShowWindow.Call(setHwnd, SW_SHOW)
	pSetForegroundWindow.Call(setHwnd)
	setVisible = true
}

func hideSettings() {
	pShowWindow.Call(setHwnd, SW_HIDE)
	setVisible = false
	setHidden = time.Now()
}

func settingsProc(h, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_ACTIVATE:
		if wp&0xFFFF == 0 && setVisible {
			hideSettings()
		}
		return 0
	case WM_KEYDOWN:
		if wp == 0x1B {
			hideSettings()
		}
		return 0
	case WM_LBUTTONUP:
		clickHits(setHits, lp)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, msg, wp, lp)
	return r
}
