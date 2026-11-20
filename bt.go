//go:build windows

package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Bluetooth headsets: Windows exposes the battery level it gets from the
// device (HFP battery indicator / LE battery service) as a device property.
// It sits on a service node (e.g. "Hands-Free AG"); the friendly name and the
// connected flag sit on the device node (BTHENUM\DEV_<mac> or BTHLE\DEV_<mac>).

var (
	cfgmgr32                  = syscall.NewLazyDLL("cfgmgr32.dll")
	pCM_Get_Device_IDW        = cfgmgr32.NewProc("CM_Get_Device_IDW")
	pCM_Get_DevNode_PropertyW = cfgmgr32.NewProc("CM_Get_DevNode_PropertyW")
	pSetupDiEnumDeviceInfo    = setupapi.NewProc("SetupDiEnumDeviceInfo")
)

type DEVPROPKEY struct {
	Fmtid GUID
	Pid   uint32
}

type SP_DEVINFO_DATA struct {
	CbSize    uint32
	ClassGuid GUID
	DevInst   uint32
	Reserved  uintptr
}

var (
	keyBattery   = DEVPROPKEY{GUID{0x104EA319, 0x6EE2, 0x4701, [8]byte{0xBD, 0x47, 0x8D, 0xDB, 0xF4, 0x25, 0xBB, 0xE5}}, 2}
	keyConnected = DEVPROPKEY{GUID{0x83DA6326, 0x97A6, 0x4088, [8]byte{0x94, 0x53, 0xA1, 0x92, 0x3F, 0x57, 0x3B, 0x29}}, 15}
	keyName      = DEVPROPKEY{GUID{0xb725f130, 0x47ef, 0x101a, [8]byte{0xa5, 0xf1, 0x02, 0x60, 0x8c, 0x9e, 0xeb, 0xac}}, 10}
)

const DIGCF_ALLCLASSES = 0x04

func devProp(inst uint32, key *DEVPROPKEY) ([]byte, uint32, bool) {
	var typ uint32
	buf := make([]byte, 512)
	n := uint32(len(buf))
	r, _, _ := pCM_Get_DevNode_PropertyW.Call(uintptr(inst), uintptr(unsafe.Pointer(key)), ptr(&typ), uintptr(unsafe.Pointer(&buf[0])), ptr(&n), 0)
	if r != 0 {
		return nil, 0, false
	}
	return buf[:n], typ, true
}

func devID(inst uint32) string {
	buf := make([]uint16, 512)
	if r, _, _ := pCM_Get_Device_IDW.Call(uintptr(inst), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func propString(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2))
}

type btDevNode struct {
	name      string
	connected int // -1 unknown, 0 no, 1 yes
}

var (
	btMu        sync.Mutex
	btInventory []string // what the last scan saw, for the report
)

type btBackend struct {
	podsNames []string // connected AirPods/Beats
}

func (bb *btBackend) poll() []Reading {
	bb.podsNames = nil
	set, _, _ := pSetupDiGetClassDevsW.Call(0, 0, 0, DIGCF_ALLCLASSES|DIGCF_PRESENT)
	if set == 0 || set == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer pSetupDiDestroyDeviceInfoList.Call(set)

	devs := map[string]*btDevNode{} // mac -> device node
	type batt struct {
		id    string
		name  string
		level int
	}
	var batts []batt
	for i := uintptr(0); ; i++ {
		var di SP_DEVINFO_DATA
		di.CbSize = uint32(unsafe.Sizeof(di))
		if r, _, _ := pSetupDiEnumDeviceInfo.Call(set, i, ptr(&di)); r == 0 {
			break
		}
		id := strings.ToUpper(devID(di.DevInst))
		if !strings.HasPrefix(id, "BTH") {
			continue
		}
		for _, pre := range []string{`BTHENUM\DEV_`, `BTHLE\DEV_`} {
			if strings.HasPrefix(id, pre) && len(id) >= len(pre)+12 {
				n := &btDevNode{connected: -1}
				if b, _, ok := devProp(di.DevInst, &keyName); ok {
					n.name = propString(b)
				}
				if b, _, ok := devProp(di.DevInst, &keyConnected); ok && len(b) > 0 {
					n.connected = 0
					if b[0] != 0 {
						n.connected = 1
					}
				}
				devs[id[len(pre):len(pre)+12]] = n
			}
		}
		if b, _, ok := devProp(di.DevInst, &keyBattery); ok && len(b) > 0 {
			name := ""
			if nb, _, ok := devProp(di.DevInst, &keyName); ok {
				name = propString(nb)
			}
			batts = append(batts, batt{id, name, int(b[0])})
		}
	}

	inv := []string{}
	for mac, dn := range devs {
		inv = append(inv, fmt.Sprintf("device %q mac=%s connected=%d", dn.name, mac, dn.connected))
	}
	for _, b := range batts {
		inv = append(inv, fmt.Sprintf("battery %d%% on %q (%s)", b.level, b.name, b.id))
	}
	sort.Strings(inv)
	btMu.Lock()
	btInventory = inv
	btMu.Unlock()

	for _, dn := range devs {
		if dn.connected == 1 && isApplePods(dn.name) {
			bb.podsNames = append(bb.podsNames, dn.name)
		}
	}
	sort.Strings(bb.podsNames)

	byMac := map[string]Reading{}
	for _, b := range batts {
		mac := ""
		for m := range devs {
			if strings.Contains(b.id, m) {
				mac = m
				break
			}
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(b.name, " Hands-Free AG"), " Hands-Free"))
		connected := true
		if dn := devs[mac]; dn != nil {
			if dn.name != "" {
				name = dn.name
			}
			connected = dn.connected != 0
		}
		dbg("bt %q mac=%s level=%d connected=%v (%s)", name, mac, b.level, connected, b.id)
		if !connected {
			continue
		}
		key := mac
		if key == "" {
			key = b.id
		}
		if _, dup := byMac[key]; dup {
			continue
		}
		if name == "" {
			name = "Bluetooth device"
		}
		byMac[key] = Reading{ID: "bt:" + key, Name: name, Kind: "Bluetooth", OK: true, Level: min(b.level, 100), Tested: true}
	}
	out := make([]Reading, 0, len(byMac))
	for _, r := range byMac {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
