//go:build windows

package main

// Non-HyperX 2.4 GHz dongles, ported from HeadsetControl (GPL-3.0).
// Most of these replies carry no header, so (like HeadsetControl) we pin the
// USB interface / usage page and take the first reply after flushing.

func scale(v, lo, hi int) int {
	if hi <= lo {
		return 0
	}
	return max(0, min(100, (v-lo)*100/(hi-lo)))
}

func anyReply(minLen int) func([]byte) bool {
	return func(r []byte) bool { return len(r) >= minLen }
}

// ---- SteelSeries ----

// Nova Pro Wireless: byte 6 = battery in the headset (0-8), byte 7 = the
// spare battery charging in the base station (0-8, same scale), byte 15 =
// headset status (1 offline, 2 charging by cable, 8 online).
func readNovaProWireless(q queryFn, st *chargeState) (int, int, error) {
	r, err := q(pkt(31, 0x06, 0xB0), func(r []byte) bool {
		// skip replies to other commands; accept the battery reply
		return len(r) >= 16 && !(r[0] == 0x06 && r[1] != 0xB0)
	})
	if err != nil {
		return 0, 0, err
	}
	dbg("  nova pro: headset=%d spare=%d status=%#x", r[6], r[7], r[15])
	st.parts = nil
	if r[15] == 0x01 {
		return 0, 0, errOffline
	}
	head := scale(int(r[6]), 0, 8)
	ch := 0
	if r[15] == 0x02 {
		ch = 1
	}
	st.parts = []Part{{Label: "In headset", Level: head, Charging: ch == 1}}
	if r[7] <= 8 {
		spare := scale(int(r[7]), 0, 8)
		p := Part{Label: "Spare (in base)", Level: spare, Charging: spare < 100}
		if spare >= 100 {
			p.Note = "Ready"
		}
		st.parts = append(st.parts, p)
	}
	return head, ch, nil
}

func readArctis1(q queryFn, _ *chargeState) (int, int, error) {
	r, err := q(pkt(2, 0x06, 0x12), anyReply(4))
	if err != nil {
		return 0, 0, err
	}
	if r[2] == 0x01 {
		return 0, 0, errOffline
	}
	return min(int(r[3]), 100), 0, nil
}

func readArctis7(q queryFn, _ *chargeState) (int, int, error) {
	r, err := q(pkt(2, 0x06, 0x18), anyReply(3))
	if err != nil {
		return 0, 0, err
	}
	return min(int(r[2]), 100), 0, nil
}

func readArctis9(q queryFn, _ *chargeState) (int, int, error) {
	r, err := q(pkt(2, 0x00, 0x20), anyReply(5))
	if err != nil {
		return 0, 0, err
	}
	ch := 0
	if r[4] == 0x01 {
		ch = 1
	}
	return scale(int(r[3]), 0x64, 0x9A), ch, nil
}

func readArctisProWireless(q queryFn, _ *chargeState) (int, int, error) {
	st, err := q(pkt(31, 0x41, 0xAA), anyReply(1))
	if err != nil {
		return 0, 0, err
	}
	if st[0] == 0x02 {
		return 0, 0, errOffline
	}
	r, err := q(pkt(31, 0x40, 0xAA), anyReply(1))
	if err != nil {
		return 0, 0, err
	}
	return scale(int(r[0]), 0, 4), 0, nil
}

// status query 0x00 0xB0 used by the newer models
func ssStatus(q queryFn, minLen int) ([]byte, error) {
	return q(pkt(2, 0x00, 0xB0), anyReply(minLen))
}

func readArctis7Plus(q queryFn, _ *chargeState) (int, int, error) {
	d, err := ssStatus(q, 4)
	if err != nil {
		return 0, 0, err
	}
	if d[1] == 0x01 {
		return 0, 0, errOffline
	}
	ch := 0
	if d[3] == 0x01 {
		ch = 1
	}
	return scale(int(d[2]), 0, 4), ch, nil
}

func readNova3P5(q queryFn, _ *chargeState) (int, int, error) {
	d, err := ssStatus(q, 5)
	if err != nil {
		return 0, 0, err
	}
	if d[1] == 0x02 {
		return 0, 0, errOffline
	}
	ch := 0
	if d[4] == 0x01 {
		ch = 1
	}
	return min(int(d[3]), 100), ch, nil
}

func readNova7(discrete bool) func(queryFn, *chargeState) (int, int, error) {
	return func(q queryFn, _ *chargeState) (int, int, error) {
		d, err := ssStatus(q, 4)
		if err != nil {
			return 0, 0, err
		}
		if d[3] == 0x00 {
			return 0, 0, errOffline
		}
		ch := 0
		if d[3] == 0x01 || d[3] == 0x02 {
			ch = 1
		}
		if discrete {
			return scale(int(d[2]), 0, 4), ch, nil
		}
		return min(int(d[2]), 100), ch, nil
	}
}

func readGameBuds(q queryFn, _ *chargeState) (int, int, error) {
	d, err := ssStatus(q, 7)
	if err != nil {
		return 0, 0, err
	}
	lv := -1
	if d[3] == 0x03 {
		lv = int(d[5])
	}
	if d[4] == 0x03 && (lv < 0 || int(d[6]) < lv) {
		lv = int(d[6])
	}
	if lv < 0 {
		return 0, 0, errOffline // both buds docked
	}
	return min(lv, 100), 0, nil
}

// ---- Corsair (Void family) ----

func readCorsairVoid(q queryFn, _ *chargeState) (int, int, error) {
	r, err := q(pkt(2, 0xC9, 0x64), anyReply(5))
	if err != nil {
		return 0, 0, err
	}
	switch r[4] {
	case 0:
		return 0, 0, errOffline
	case 4, 5:
		return min(int(r[2]&0x7F), 100), 1, nil
	}
	return min(int(r[2]&0x7F), 100), 0, nil
}

// ---- MCHOSE X9 ----

func readMchoseX9(q queryFn, _ *chargeState) (int, int, error) {
	r, err := q(pkt(64, 0x55, 0x65, 0x01), func(r []byte) bool {
		return len(r) >= 4 && r[0] == 0x55 && r[1] == 0x65 && r[3] == 0x02
	})
	if err != nil {
		return 0, 0, err
	}
	return min(int(r[2]), 100), 0, nil
}

// ---- Sony INZONE (H5, H9 II): HCI-style commands with checksum ----

func readSonyInzone(q queryFn, st *chargeState) (int, int, error) {
	st.tid++
	if st.tid <= 1 {
		st.tid = 2
	}
	tid := st.tid
	b := make([]byte, 64)
	b[0], b[1] = 0x02, 12 // report id, HCI length (no payload)
	b[2], b[3], b[4], b[5] = 0x01, 0x00, 0xFC, 8
	b[6], b[7] = 0x96, 0xC3
	b[8], b[9], b[10] = 0x41, 0x04, 0x01 // PC->receiver, BATTERY_INFO, GET
	b[11], b[12] = byte(tid), byte(tid>>8)
	sum := 0
	for i := 6; i <= 12; i++ {
		sum += int(b[i])
	}
	b[13] = byte(sum)
	r, err := q(b, func(r []byte) bool {
		if len(r) < 16 || r[0] != 0x02 || r[2] != 0x04 || r[3] != 0xFF || r[5] != 0 || r[6] != 0x96 || r[7] != 0xC3 {
			return false
		}
		n := int(r[1])
		if n < 12 || n+1 >= len(r) || r[8]>>4 != 0x1 {
			return false
		}
		s := 0
		for i := 5; i <= n; i++ {
			s += int(r[i])
		}
		return byte(s) == r[n+1] && r[9] == 0x04 && uint16(r[11])|uint16(r[12])<<8 == tid &&
			(r[10] == 0x10 || r[10] == 0xA0) && n > 13
	})
	if err != nil {
		return 0, 0, err
	}
	charger, pct := r[13], r[14]
	if pct == 0xFF {
		return 0, 0, errOffline
	}
	if pct > 100 {
		return 0, 0, errTimeout
	}
	ch := 0
	if charger != 0 {
		ch = 1
	}
	return int(pct), ch, nil
}

const (
	vidSteelSeries = 0x1038
	vidCorsair     = 0x1b1c
	vidSony        = 0x054c
)

var brandProtocols = []protocol{
	{name: "SteelSeries Arctis Nova Pro Wireless", vid: vidSteelSeries, pids: []uint16{0x12e0, 0x12e5}, mi: 4 + 1, read: readNovaProWireless, tested: true},
	{name: "SteelSeries Arctis Nova 7", vid: vidSteelSeries, pids: []uint16{0x22a1, 0x227e, 0x2258, 0x229e, 0x22ad, 0x22a9, 0x22a5, 0x22a7, 0x2298}, mi: 3 + 1, usage: 0xffc0, read: readNova7(false)},
	{name: "SteelSeries Arctis Nova 7", vid: vidSteelSeries, pids: []uint16{0x2202, 0x2206, 0x223a, 0x227a, 0x22a4, 0x220a}, mi: 3 + 1, usage: 0xffc0, read: readNova7(true)},
	{name: "SteelSeries Arctis Nova 5", vid: vidSteelSeries, pids: []uint16{0x2232, 0x2253}, mi: 3 + 1, usage: 0xffc0, read: readNova3P5},
	{name: "SteelSeries Arctis Nova 3P Wireless", vid: vidSteelSeries, pids: []uint16{0x2269, 0x226d}, mi: 3 + 1, usage: 0xffc0, read: readNova3P5},
	{name: "SteelSeries Arctis 7+", vid: vidSteelSeries, pids: []uint16{0x220e, 0x2212, 0x2216, 0x2236}, mi: 3 + 1, usage: 0xffc0, read: readArctis7Plus},
	{name: "SteelSeries Arctis 7", vid: vidSteelSeries, pids: []uint16{0x1260, 0x12ad, 0x1252, 0x1280}, mi: 5 + 1, usage: 0xff43, read: readArctis7},
	{name: "SteelSeries Arctis 1 Wireless", vid: vidSteelSeries, pids: []uint16{0x12b3, 0x12b6, 0x12d7, 0x12d5}, mi: 3 + 1, usage: 0xff43, read: readArctis1},
	{name: "SteelSeries Arctis 9", vid: vidSteelSeries, pids: []uint16{0x12c2, 0x12c4}, mi: 0 + 1, read: readArctis9},
	{name: "SteelSeries Arctis Pro Wireless", vid: vidSteelSeries, pids: []uint16{0x1290}, mi: 0 + 1, read: readArctisProWireless},
	{name: "SteelSeries Arctis GameBuds", vid: vidSteelSeries, pids: []uint16{0x230a}, mi: 3 + 1, usage: 0xffc0, read: readGameBuds},
	{name: "Corsair Void Wireless", vid: vidCorsair, pids: []uint16{0x1b1c, 0x1b27, 0x0a14, 0x0a16, 0x0a17, 0x0a1d, 0x0a1a, 0x1b2a, 0x1b23, 0x1b29, 0x0a55, 0x0a51, 0x0a52, 0x0a38, 0x0a4f, 0x0a2b, 0x0a75, 0x0a56}, mi: 3 + 1, usage: 0xffc5, read: readCorsairVoid},
	{name: "Sony INZONE H9 II", vid: vidSony, pids: []uint16{0x0fa8}, mi: 5 + 1, usage: 0xff04, read: readSonyInzone},
	{name: "Sony INZONE H5", vid: vidSony, pids: []uint16{0x0ebf}, usage: 0xff04, read: readSonyInzone},
	{name: "MCHOSE X9", vid: 0x3837, pids: []uint16{0x6045}, mi: 0 + 1, usage: 0xff90, read: readMchoseX9},
}

func init() { protocols = append(protocols, brandProtocols...) }
