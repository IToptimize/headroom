//go:build windows

package main

import "time"

// Reading is one device's raw state from a backend.
type Reading struct {
	ID       string // stable id, e.g. "hid:0951:1723" or "bt:C8F7509F0B12"
	Name     string
	Kind     string // "USB" or "Bluetooth"
	Tested   bool   // support confirmed on real hardware
	Dongle   bool
	OK       bool // device answered with a level
	Level    int  // 0-100, -1 unknown
	Charging int  // 0 not, 1 charging, 2 full
	Err      string
	Detail   string // e.g. "L 80% · R 70% · Case 40%"
	Parts    []Part // per-part levels (AirPods: left, right, case)
}

// Part is one battery inside a device, e.g. an AirPods bud.
type Part struct {
	Label    string
	Level    int // -1 unknown
	Charging bool
	Note     string // shown when Level is unknown
	Sub      string // small text under the %, e.g. "≈ 20 h left" / "≈ 45 min to full"
	Tint     int    // 0 battery colours; Claude limits: 1 session, 2 weekly, 3 model limit
}

// Poller asks every backend for its devices.
type Poller struct {
	hid      hidBackend
	bt       btBackend
	pods     podsScanner
	podsGone time.Time
}

func (p *Poller) Poll() []Reading {
	out := p.hid.poll()
	bt := p.bt.poll()
	names := p.bt.podsNames
	if len(names) > 0 {
		p.pods.start()
		p.podsGone = time.Time{}
		for _, n := range names {
			out = append(out, p.pods.podsReading(n))
		}
		// Windows' own value for AirPods (if any) is replaced by the broadcast one
		kept := bt[:0]
		for _, r := range bt {
			if !isApplePods(r.Name) {
				kept = append(kept, r)
			}
		}
		bt = kept
	} else {
		if p.podsGone.IsZero() {
			p.podsGone = time.Now()
		} else if time.Since(p.podsGone) > time.Minute {
			p.pods.halt() // stop scanning a minute after AirPods disconnect
		}
	}
	return append(out, bt...)
}
