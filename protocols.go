//go:build windows

package main

import "errors"

// Protocols for HyperX wireless dongles. Packet layouts come from the
// open-source projects HeadsetControl (GPL-3.0) and HyperHeadset (MIT);
// the Cloud Flight voltage curve and charge detection are our own.

var errOffline = errors.New("headset off or out of range")

// q sends pkt and returns the first input report that match accepts.
type queryFn func(pkt []byte, match func(r []byte) bool) ([]byte, error)

type protocol struct {
	name   string
	vid    uint16
	pids   []uint16
	prep   bool   // read an input report before each write (Kingston Cloud II dongle)
	tested bool   // confirmed on real hardware by us
	mi     int    // USB interface number + 1 to use (0 = try all)
	usage  uint16 // HID usage page to use (0 = any)
	// read returns level (-1 unknown) and charging (0 no, 1 charging, 2 full).
	read func(q queryFn, st *chargeState) (level, charging int, err error)
}

func pkt(n int, head ...byte) []byte {
	p := make([]byte, n)
	copy(p, head)
	return p
}

// ---- Cloud Flight / Flight S (voltage based) ----

func readFlight(q queryFn, st *chargeState) (int, int, error) {
	r, err := q(pkt(20, 0x21, 0xFF, 0x05), func(r []byte) bool {
		return len(r) > 4 && r[0] == 0x21 && r[1] == 0xFF && r[2] == 0x05
	})
	if err != nil {
		return 0, 0, err
	}
	mv := int(r[3])<<8 | int(r[4])
	ch := st.update(mv)
	dbg("  voltage %d mV charging=%v rest=%d", mv, ch, st.restMv)
	if ch {
		return -1, 1, nil
	}
	return flightLevel(mv), 0, nil
}

// ---- Cloud II Wireless, Kingston dongle ----

func kingstonCmd(cmd byte) []byte {
	return pkt(62, 0x06, 0x00, 0x02, 0x00, 0x9A, 0x00, 0x00, 0x68, 0x4A, 0x8E, 0x0A, 0x00, 0x00, 0x00, 0xBB, cmd)
}

func readKingstonCloud2(q queryFn, _ *chargeState) (int, int, error) {
	m := func(cmd byte) func([]byte) bool {
		return func(r []byte) bool { return len(r) > 7 && r[0] == 0x0B && r[2] == 0xBB && r[3] == cmd }
	}
	lv, err := q(kingstonCmd(0x02), m(0x02))
	if err != nil {
		return 0, 0, err
	}
	ch, err := q(kingstonCmd(0x03), m(0x03))
	if err != nil {
		return int(lv[7]), 0, nil
	}
	return int(lv[7]), int(ch[4]), nil
}

// ---- Cloud II Wireless, HP dongle (incl. DTS) ----

func readHPCloud2(q queryFn, _ *chargeState) (int, int, error) {
	m := func(cmd byte) func([]byte) bool {
		return func(r []byte) bool { return len(r) > 7 && r[0] == 0x06 && r[1] == 0xFF && r[2] == 0xBB && r[3] == cmd }
	}
	lv, err := q(pkt(52, 0x06, 0xFF, 0xBB, 0x02), m(0x02))
	if err != nil {
		return 0, 0, err
	}
	ch, err := q(pkt(52, 0x06, 0xFF, 0xBB, 0x03), m(0x03))
	if err != nil {
		return int(lv[7]), 0, nil
	}
	return int(lv[7]), int(ch[4]), nil
}

// ---- Cloud Alpha Wireless ----

func readAlphaWireless(q queryFn, _ *chargeState) (int, int, error) {
	m := func(cmds ...byte) func([]byte) bool {
		return func(r []byte) bool {
			if len(r) < 4 || r[0] != 0x21 || r[1] != 0xBB {
				return false
			}
			for _, c := range cmds {
				if r[2] == c {
					return true
				}
			}
			return false
		}
	}
	conn, err := q(pkt(64, 0x21, 0xBB, 0x03), m(0x03, 0x24))
	if err != nil {
		return 0, 0, err
	}
	if conn[3] == 0x01 {
		return 0, 0, errOffline
	}
	lv, err := q(pkt(64, 0x21, 0xBB, 0x0B), m(0x0B, 0x25))
	if err != nil {
		return 0, 0, err
	}
	charging := 0
	if ch, err := q(pkt(64, 0x21, 0xBB, 0x0C), m(0x0C, 0x26)); err == nil {
		charging = int(ch[3])
	}
	return int(lv[3]), charging, nil
}

// ---- Cloud Alpha 2 Wireless (base station) ----

func readAlpha2(q queryFn, _ *chargeState) (int, int, error) {
	conn, err := q(pkt(64, 0x52, 0x01), func(r []byte) bool { return len(r) > 3 && r[0] == 0x53 && r[1] == 0x01 })
	if err != nil {
		return 0, 0, err
	}
	if conn[2] == 0 {
		return 0, 0, errOffline
	}
	r, err := q(pkt(64, 0x50, 0x02), func(r []byte) bool { return len(r) > 3 && r[0] == 0x51 && r[1] == 0x02 })
	if err != nil {
		return 0, 0, err
	}
	if r[2] > 100 {
		return 0, 0, errors.New("battery out of range")
	}
	ch := 0
	if r[3] == 1 {
		ch = 1
	}
	return int(r[2]), ch, nil
}

// ---- Cloud III Wireless / Cloud II Core Wireless (0x66 protocol) ----

func readCloud3(q queryFn, _ *chargeState) (int, int, error) {
	m := func(cmd, resp byte) func([]byte) bool {
		return func(r []byte) bool { return len(r) > 4 && r[0] == 0x66 && (r[1] == cmd || r[1] == resp) }
	}
	conn, err := q(pkt(64, 0x66, 0x82), m(0x82, 0x0B))
	if err != nil {
		return 0, 0, err
	}
	if conn[2] != 1 {
		return 0, 0, errOffline
	}
	lv, err := q(pkt(64, 0x66, 0x89), m(0x89, 0x0D))
	if err != nil {
		return 0, 0, err
	}
	if lv[2] == 0 && lv[3] == 0 {
		return 0, 0, errOffline
	}
	charging := 0
	if ch, err := q(pkt(64, 0x66, 0x8A), m(0x8A, 0x0C)); err == nil {
		charging = int(ch[2])
	}
	return int(lv[4]), charging, nil
}

// ---- Cloud III S Wireless ----

func readCloud3S(q queryFn, _ *chargeState) (int, int, error) {
	cmd := func(c byte) ([]byte, error) {
		return q(pkt(64, 0x0C, 0x02, 0x03, 0x01, 0x00, c), func(r []byte) bool { return len(r) > 6 && r[0] == 0x0C && r[5] == c })
	}
	conn, err := cmd(0x02)
	if err != nil {
		return 0, 0, err
	}
	if conn[6] != 2 {
		return 0, 0, errOffline
	}
	lv, err := cmd(0x06)
	if err != nil {
		return 0, 0, err
	}
	if lv[6] == 0xFF {
		return 0, 0, errOffline
	}
	charging := 0
	if ch, err := cmd(0x48); err == nil && ch[6] != 0xFF {
		charging = int(ch[6])
	}
	return int(lv[6]), charging, nil
}

var protocols = []protocol{
	{name: "HyperX Cloud Flight Wireless", vid: 0x0951, pids: []uint16{0x1723, 0x16c4, 0x1749, 0x0e90}, read: readFlight, tested: true},
	{name: "HyperX Cloud Flight Wireless", vid: 0x03f0, pids: []uint16{0x0e90, 0x1749}, read: readFlight},
	{name: "HyperX Cloud II Wireless", vid: 0x0951, pids: []uint16{0x1718, 0x0b92, 0x16ea, 0x16eb}, read: readKingstonCloud2, prep: true},
	{name: "HyperX Cloud II Wireless", vid: 0x03f0, pids: []uint16{0x0696, 0x018b, 0x0d93}, read: readHPCloud2},
	{name: "HyperX Cloud Alpha Wireless", vid: 0x03f0, pids: []uint16{0x098d, 0x1743, 0x1765}, read: readAlphaWireless},
	{name: "HyperX Cloud Alpha 2 Wireless", vid: 0x03f0, pids: []uint16{0x08be}, read: readAlpha2},
	{name: "HyperX Cloud III Wireless", vid: 0x03f0, pids: []uint16{0x05b7, 0x0c9d}, read: readCloud3},
	{name: "HyperX Cloud II Core Wireless", vid: 0x03f0, pids: []uint16{0x069f, 0x0995}, read: readCloud3},
	{name: "HyperX Cloud III S Wireless", vid: 0x03f0, pids: []uint16{0x06be, 0x02cc}, read: readCloud3S},
}

func findProtocol(vid, pid uint16) *protocol {
	for i := range protocols {
		if protocols[i].vid != vid {
			continue
		}
		for _, p := range protocols[i].pids {
			if p == pid {
				return &protocols[i]
			}
		}
	}
	return nil
}
