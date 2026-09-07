//go:build windows

package winnet

import (
	"fmt"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCreateUnicastIpAddressEntry     = modIphlpapi.NewProc("CreateUnicastIpAddressEntry")
	procSetUnicastIpAddressEntry        = modIphlpapi.NewProc("SetUnicastIpAddressEntry")
	procInitializeUnicastIpAddressEntry = modIphlpapi.NewProc("InitializeUnicastIpAddressEntry")
	procInitializeIpInterfaceEntry      = modIphlpapi.NewProc("InitializeIpInterfaceEntry")
	procSetIpInterfaceEntry             = modIphlpapi.NewProc("SetIpInterfaceEntry")
)

// UnicastIPv4Info is the OS view of a tunnel IPv4 address.
type UnicastIPv4Info struct {
	Addr               netip.Addr
	OnLinkPrefixLength uint8
	InterfaceIndex     uint32
	InterfaceLUID      uint64
	DadState           uint32
	SkipAsSource       bool
	ValidLifetime      uint32
	PreferredLifetime  uint32
}

func (u UnicastIPv4Info) DadStateName() string {
	switch u.DadState {
	case windows.IpDadStateInvalid:
		return "Invalid"
	case windows.IpDadStateTentative:
		return "Tentative"
	case windows.IpDadStateDuplicate:
		return "Duplicate"
	case windows.IpDadStateDeprecated:
		return "Deprecated"
	case windows.IpDadStatePreferred:
		return "Preferred"
	default:
		return fmt.Sprintf("DadState(%d)", u.DadState)
	}
}

// IpInterfaceSnapshot is safe diagnostic state for MIB_IPINTERFACE_ROW (no secrets).
type IpInterfaceSnapshot struct {
	Family           uint16
	InterfaceIndex   uint32
	InterfaceLUID    uint64
	SitePrefixLength uint32
	WeakHostSend     uint8
	WeakHostReceive  uint8
	Metric           uint32
	NlMtu            uint32
	SkippedSet       bool
	SkipReason       string
}

func snapshotIpInterface(row *windows.MibIpInterfaceRow) IpInterfaceSnapshot {
	return IpInterfaceSnapshot{
		Family:           row.Family,
		InterfaceIndex:   row.InterfaceIndex,
		InterfaceLUID:    row.InterfaceLuid,
		SitePrefixLength: row.SitePrefixLength,
		WeakHostSend:     row.WeakHostSend,
		WeakHostReceive:  row.WeakHostReceive,
		Metric:           row.Metric,
		NlMtu:            row.NlMtu,
	}
}

// FindUnicastIPv4OnLUID locates addr on the given interface LUID.
func FindUnicastIPv4OnLUID(luid uint64, addr netip.Addr) (UnicastIPv4Info, bool, error) {
	if !addr.IsValid() || !addr.Is4() {
		return UnicastIPv4Info{}, false, fmt.Errorf("winnet: IPv4 addr required")
	}
	want := addr.Unmap()
	var table *windows.MibUnicastIpAddressTable
	if err := windows.GetUnicastIpAddressTable(windows.AF_INET, &table); err != nil {
		return UnicastIPv4Info{}, false, fmt.Errorf("winnet: GetUnicastIpAddressTable: %w", err)
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	for i := range rows {
		row := &rows[i]
		if row.InterfaceLuid != luid {
			continue
		}
		got, ok := parseUnicastRowAddr(row)
		if !ok || got.Unmap() != want {
			continue
		}
		return unicastInfoFromRow(row, got), true, nil
	}
	return UnicastIPv4Info{}, false, nil
}

// EnsureTunnelUnicastIPv4 makes the TypeConfig VPN address Preferred with SkipAsSource=false.
// Call after netsh address apply (or instead when create is needed).
func EnsureTunnelUnicastIPv4(luid uint64, ifIndex uint32, addr netip.Addr, prefixLen int) (UnicastIPv4Info, error) {
	if !addr.IsValid() || !addr.Is4() {
		return UnicastIPv4Info{}, fmt.Errorf("winnet: IPv4 addr required")
	}
	if prefixLen < 0 || prefixLen > 32 {
		return UnicastIPv4Info{}, fmt.Errorf("winnet: bad prefix %d", prefixLen)
	}
	addr = addr.Unmap()

	info, found, err := FindUnicastIPv4OnLUID(luid, addr)
	if err != nil {
		return UnicastIPv4Info{}, err
	}
	if !found {
		if err := createUnicastIPv4(luid, ifIndex, addr, prefixLen); err != nil {
			return UnicastIPv4Info{}, err
		}
	}
	if err := forcePreferredUnicastIPv4(luid, ifIndex, addr, prefixLen); err != nil {
		return UnicastIPv4Info{}, err
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		info, found, err = FindUnicastIPv4OnLUID(luid, addr)
		if err != nil {
			return UnicastIPv4Info{}, err
		}
		if found && info.DadState == windows.IpDadStatePreferred && !info.SkipAsSource &&
			info.ValidLifetime != 0 && info.PreferredLifetime != 0 {
			return info, nil
		}
		if time.Now().After(deadline) {
			if !found {
				return UnicastIPv4Info{}, fmt.Errorf("winnet: tunnel IPv4 %s missing on luid=%d", addr, luid)
			}
			return info, fmt.Errorf("winnet: tunnel IPv4 %s not ready dad=%s skipAsSource=%v valid=%d preferred=%d",
				addr, info.DadStateName(), info.SkipAsSource, info.ValidLifetime, info.PreferredLifetime)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// prepareIPv4IpInterfaceRowForSet mutates a GetIpInterfaceEntry row in-place so it
// is valid for SetIpInterfaceEntry without wiping unrelated interface properties.
//
// Microsoft requirement (IPv4): SitePrefixLength MUST be 0 before SetIpInterfaceEntry,
// otherwise ERROR_INVALID_PARAMETER (87). Only intended hardening fields are changed.
func prepareIPv4IpInterfaceRowForSet(row *windows.MibIpInterfaceRow) {
	row.Family = windows.AF_INET
	row.SitePrefixLength = 0
	row.WeakHostSend = 0
	row.WeakHostReceive = 0
	// Metric, NlMtu, RouterDiscoveryBehavior, ZoneIndices, etc. left as returned by Get.
}

// HardenResult captures interface hardening before/after for diagnostics.
type HardenResult struct {
	Before     IpInterfaceSnapshot
	After      IpInterfaceSnapshot
	SkippedSet bool
	SkipReason string
}

// HardenTunnelIPv4Interface disables weak-host send/receive on the Wintun IPv4 stack
// so Windows must use the tunnel address as source for packets exiting the adapter.
// metricHint is retained for API compatibility; Metric is not forced (avoids clobbering OS values).
func HardenTunnelIPv4Interface(luid uint64, ifIndex uint32, metricHint uint32) (HardenResult, error) {
	_ = metricHint
	row, err := getIPv4IpInterfaceRow(luid, ifIndex)
	if err != nil {
		return HardenResult{}, err
	}
	before := snapshotIpInterface(&row)

	// Already hardened — skip SetIpInterfaceEntry (avoids ERROR 87 on no-op paths too).
	if row.WeakHostSend == 0 && row.WeakHostReceive == 0 {
		before.SkippedSet = true
		before.SkipReason = "weak_host_already_disabled"
		return HardenResult{
			Before:     before,
			After:      before,
			SkippedSet: true,
			SkipReason: "weak_host_already_disabled",
		}, nil
	}

	prepareIPv4IpInterfaceRowForSet(&row)
	r1, _, e1 := procSetIpInterfaceEntry.Call(uintptr(unsafe.Pointer(&row)))
	if r1 != 0 {
		return HardenResult{Before: before}, formatSetIpInterfaceEntryError(r1, e1, before, 0)
	}

	afterRow, err := getIPv4IpInterfaceRow(luid, ifIndex)
	if err != nil {
		return HardenResult{Before: before}, fmt.Errorf("winnet: GetIpInterfaceEntry after Set: %w", err)
	}
	after := snapshotIpInterface(&afterRow)
	if after.WeakHostSend != 0 {
		return HardenResult{Before: before, After: after}, fmt.Errorf(
			"winnet: WeakHostSend still %d after SetIpInterfaceEntry (want 0) ifIndex=%d luid=%d",
			after.WeakHostSend, after.InterfaceIndex, after.InterfaceLUID)
	}
	if after.WeakHostReceive != 0 {
		return HardenResult{Before: before, After: after}, fmt.Errorf(
			"winnet: WeakHostReceive still %d after SetIpInterfaceEntry (want 0) ifIndex=%d luid=%d",
			after.WeakHostReceive, after.InterfaceIndex, after.InterfaceLUID)
	}
	return HardenResult{Before: before, After: after}, nil
}

func getIPv4IpInterfaceRow(luid uint64, ifIndex uint32) (windows.MibIpInterfaceRow, error) {
	var row windows.MibIpInterfaceRow
	procInitializeIpInterfaceEntry.Call(uintptr(unsafe.Pointer(&row)))
	row.Family = windows.AF_INET
	row.InterfaceLuid = luid
	row.InterfaceIndex = ifIndex
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return windows.MibIpInterfaceRow{}, fmt.Errorf("winnet: GetIpInterfaceEntry: %w", err)
	}
	return row, nil
}

func formatSetIpInterfaceEntryError(r1 uintptr, e1 error, before IpInterfaceSnapshot, desiredWeakSend uint8) error {
	msg := ""
	switch v := e1.(type) {
	case windows.Errno:
		msg = v.Error() // FormatMessage from system
	case interface{ Error() string }:
		if e1 != nil && e1 != windows.ERROR_SUCCESS {
			msg = v.Error()
		}
	}
	return fmt.Errorf(
		"winnet: SetIpInterfaceEntry rc=%d msg=%q family=%d ifIndex=%d luid=%d sitePrefixLength=%d weakHostSend_before=%d weakHostSend_desired=%d weakHostReceive_before=%d metric=%d nlMtu=%d",
		r1, msg,
		before.Family, before.InterfaceIndex, before.InterfaceLUID,
		before.SitePrefixLength, before.WeakHostSend, desiredWeakSend,
		before.WeakHostReceive, before.Metric, before.NlMtu,
	)
}

func createUnicastIPv4(luid uint64, ifIndex uint32, addr netip.Addr, prefixLen int) error {
	var row windows.MibUnicastIpAddressRow
	procInitializeUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	if err := putUnicastRowAddr(&row, addr); err != nil {
		return err
	}
	row.InterfaceLuid = luid
	row.InterfaceIndex = ifIndex
	row.OnLinkPrefixLength = uint8(prefixLen)
	row.PrefixOrigin = 1 // IpPrefixOriginManual
	row.SuffixOrigin = 1 // IpSuffixOriginManual
	const infinite uint32 = 0xffffffff
	row.ValidLifetime = infinite
	row.PreferredLifetime = infinite
	row.SkipAsSource = 0
	row.DadState = windows.IpDadStatePreferred
	r1, _, e1 := procCreateUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	if r1 != 0 {
		// Already exists is fine — forcePreferred will repair flags.
		if errno, ok := e1.(windows.Errno); ok && errno == windows.ERROR_OBJECT_ALREADY_EXISTS {
			return nil
		}
		if e1 != windows.ERROR_SUCCESS {
			return fmt.Errorf("winnet: CreateUnicastIpAddressEntry: %w", e1)
		}
		return fmt.Errorf("winnet: CreateUnicastIpAddressEntry: %d", r1)
	}
	return nil
}

func forcePreferredUnicastIPv4(luid uint64, ifIndex uint32, addr netip.Addr, prefixLen int) error {
	var row windows.MibUnicastIpAddressRow
	if err := putUnicastRowAddr(&row, addr); err != nil {
		return err
	}
	row.InterfaceLuid = luid
	row.InterfaceIndex = ifIndex
	if err := windows.GetUnicastIpAddressEntry(&row); err != nil {
		return fmt.Errorf("winnet: GetUnicastIpAddressEntry: %w", err)
	}
	const infinite uint32 = 0xffffffff
	row.OnLinkPrefixLength = uint8(prefixLen)
	row.ValidLifetime = infinite
	row.PreferredLifetime = infinite
	row.SkipAsSource = 0
	row.DadState = windows.IpDadStatePreferred
	r1, _, e1 := procSetUnicastIpAddressEntry.Call(uintptr(unsafe.Pointer(&row)))
	if r1 != 0 {
		if e1 != windows.ERROR_SUCCESS {
			return fmt.Errorf("winnet: SetUnicastIpAddressEntry: %w", e1)
		}
		return fmt.Errorf("winnet: SetUnicastIpAddressEntry: %d", r1)
	}
	return nil
}

func parseUnicastRowAddr(row *windows.MibUnicastIpAddressRow) (netip.Addr, bool) {
	// Address is SOCKADDR_INET stored in RawSockaddrInet6 union.
	family := row.Address.Family
	if family == windows.AF_INET {
		raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
		return netip.AddrFrom4(raw.Addr), true
	}
	return netip.Addr{}, false
}

func putUnicastRowAddr(row *windows.MibUnicastIpAddressRow, addr netip.Addr) error {
	if !addr.Is4() {
		return fmt.Errorf("winnet: IPv4 required")
	}
	raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
	*raw = windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: addr.As4()}
	return nil
}

func unicastInfoFromRow(row *windows.MibUnicastIpAddressRow, addr netip.Addr) UnicastIPv4Info {
	return UnicastIPv4Info{
		Addr:               addr,
		OnLinkPrefixLength: row.OnLinkPrefixLength,
		InterfaceIndex:     row.InterfaceIndex,
		InterfaceLUID:      row.InterfaceLuid,
		DadState:           row.DadState,
		SkipAsSource:       row.SkipAsSource != 0,
		ValidLifetime:      row.ValidLifetime,
		PreferredLifetime:  row.PreferredLifetime,
	}
}
