//go:build windows

package main

import (
	"strconv"
	"time"
)

const appVersion = "1.0 beta"

// Settings persisted in HKCU\Software\Headroom.
type config struct {
	Theme     int // 0 system, 1 light, 2 dark
	ColorMode int // 0 battery colours, 1 accent colour
	Accent    int // index into accents
	Icon      int // 0 ring, 1 ring + %, 2 pie
	Compact   bool
	Popups    map[int]bool // level -> enabled
	Interval  int          // seconds between normal checks
}

var popupLevels = []int{50, 20, 10, 5}

var accents = []color{
	{59, 130, 246, 1}, // blue
	{34, 197, 94, 1},  // green
	{168, 85, 247, 1}, // purple
	{236, 72, 153, 1}, // pink
	{249, 115, 22, 1}, // orange
	{20, 184, 166, 1}, // teal
}

var cfg config

func regInt(name string, def int) int {
	if v, err := strconv.Atoi(regGet("cfg|" + name)); err == nil {
		return v
	}
	return def
}

func loadConfig() {
	cfg = config{
		Theme:     regInt("Theme", 0),
		ColorMode: regInt("ColorMode", 0),
		Accent:    regInt("Accent", 0),
		Icon:      regInt("Icon", 0),
		Compact:   regInt("Compact", 0) == 1,
		Interval:  regInt("Interval", 60),
		Popups:    map[int]bool{},
	}
	for _, l := range popupLevels {
		def := 0
		if l == 50 || l == 10 || l == 5 {
			def = 1
		}
		cfg.Popups[l] = regInt("Popup"+strconv.Itoa(l), def) == 1
	}
	if cfg.Accent < 0 || cfg.Accent >= len(accents) {
		cfg.Accent = 0
	}
	if cfg.Interval < 15 {
		cfg.Interval = 60
	}
}

func saveConfig() {
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	regSet("cfg|Theme", strconv.Itoa(cfg.Theme))
	regSet("cfg|ColorMode", strconv.Itoa(cfg.ColorMode))
	regSet("cfg|Accent", strconv.Itoa(cfg.Accent))
	regSet("cfg|Icon", strconv.Itoa(cfg.Icon))
	regSet("cfg|Compact", b(cfg.Compact))
	regSet("cfg|Interval", strconv.Itoa(cfg.Interval))
	for _, l := range popupLevels {
		regSet("cfg|Popup"+strconv.Itoa(l), b(cfg.Popups[l]))
	}
}

func pollEvery() time.Duration { return time.Duration(cfg.Interval) * time.Second }

func accentColor() color { return accents[cfg.Accent] }

// panelLight: theme for our own panels (the tray icon follows the taskbar).
func panelLight() bool {
	switch cfg.Theme {
	case 1:
		return true
	case 2:
		return false
	}
	return lightTheme()
}
