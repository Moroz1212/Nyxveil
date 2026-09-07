//go:build windows

package winnet

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modIphlpapi              = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetBestRoute2        = modIphlpapi.NewProc("GetBestRoute2")
	procGetIpForwardTable2   = modIphlpapi.NewProc("GetIpForwardTable2")
	procFreeMibTable         = modIphlpapi.NewProc("FreeMibTable")
	procCreateIpForwardEntry2 = modIphlpapi.NewProc("CreateIpForwardEntry2")
	procDeleteIpForwardEntry2 = modIphlpapi.NewProc("DeleteIpForwardEntry2")
	procGetIfEntry2          = modIphlpapi.NewProc("GetIfEntry2")
)

const (
	afINET  = 2
	afINET6 = 23
)

// DefaultRoute is the active IPv4 default route from IP Helper (not route.exe text).
type DefaultRoute struct {
	Present        bool
	NextHop        netip.Addr
	BestSource     netip.Addr
	InterfaceIndex uint32
	InterfaceLUID  uint64
	Metric         uint32
}

// RouteSpec describes an IPv4 route to add/delete.
type RouteSpec struct {
	Destination    netip.Prefix
	NextHop        netip.Addr
	InterfaceIndex uint32
	InterfaceLUID  uint64
	Metric         uint32
}

type sockaddrInet struct {
	Family uint16
	Port   uint16
	Addr   [4]byte
	Zero   [8]byte
}

type sockaddrInet6 struct {
	Family   uint16
	Port     uint16
	FlowInfo uint32
	Addr     [16]byte
	ScopeId  uint32
}

// MIB_IPFORWARD_ROW2 (subset; layout must match Windows SDK).
type mibIpForwardRow2 struct {
	InterfaceLuid        uint64
	InterfaceIndex       uint32
	DestinationPrefix    struct {
		Prefix       [28]byte // SOCKADDR_INET
		PrefixLength uint8
		_            [3]byte
	}
	NextHop              [28]byte // SOCKADDR_INET
	SitePrefixLength     uint8
	_                    [3]byte
	ValidLifetime         uint32
	PreferredLifetime     uint32
	Metric               uint32
	Protocol             uint32
	Loopback             uint8
	AutoconfigureAddress uint8
	Publish              uint8
	Immortal             uint8
	Age                  uint32
	Origin               uint32
}

type mibIpForwardTable2 struct {
	NumEntries uint32
	Table      [1]mibIpForwardRow2
}

// GetIPv4DefaultRoute resolves the effective outbound IPv4 path via GetBestRoute2
// toward a public probe address (interface+route metric policy), falling back to
// lowest-metric 0.0.0.0/0 from GetIpForwardTable2.
func GetIPv4DefaultRoute() (DefaultRoute, error) {
	probe := netip.AddrFrom4([4]byte{1, 1, 1, 1})
	if br, err := ResolveIPv4BestRoute(probe); err == nil && br.Present {
		return br, nil
	}
	return getIPv4DefaultRouteFromTable()
}

// ResolveIPv4BestRoute uses GetBestRoute2 for the actual outbound path to dest.
func ResolveIPv4BestRoute(dest netip.Addr) (DefaultRoute, error) {
	if !dest.IsValid() || !dest.Is4() {
		return DefaultRoute{}, fmt.Errorf("winnet: IPv4 destination required")
	}
	var destSA [28]byte
	if err := putAddr(&destSA, dest); err != nil {
		return DefaultRoute{}, err
	}
	var row mibIpForwardRow2
	var bestSrc [28]byte
	r1, _, e1 := procGetBestRoute2.Call(
		0, // InterfaceLuid = NULL
		0, // InterfaceIndex = 0
		0, // SourceAddress = NULL
		uintptr(unsafe.Pointer(&destSA)),
		0, // AddressSortOptions
		uintptr(unsafe.Pointer(&row)),
		uintptr(unsafe.Pointer(&bestSrc)),
	)
	if r1 != 0 {
		if e1 != windows.ERROR_SUCCESS {
			return DefaultRoute{}, fmt.Errorf("winnet: GetBestRoute2: %w", e1)
		}
		return DefaultRoute{}, fmt.Errorf("winnet: GetBestRoute2: %d", r1)
	}
	nh, ok := parseAddr(&row.NextHop)
	if !ok || !nh.Is4() {
		return DefaultRoute{}, fmt.Errorf("winnet: GetBestRoute2: invalid next hop")
	}
	src, _ := parseAddr(&bestSrc)
	return DefaultRoute{
		Present:        true,
		NextHop:        nh,
		BestSource:     src,
		InterfaceIndex: row.InterfaceIndex,
		InterfaceLUID:  row.InterfaceLuid,
		Metric:         row.Metric,
	}, nil
}

func getIPv4DefaultRouteFromTable() (DefaultRoute, error) {
	var table *mibIpForwardTable2
	r1, _, e1 := procGetIpForwardTable2.Call(uintptr(afINET), uintptr(unsafe.Pointer(&table)))
	if r1 != 0 {
		if e1 != windows.ERROR_SUCCESS {
			return DefaultRoute{}, fmt.Errorf("winnet: GetIpForwardTable2: %w", e1)
		}
		return DefaultRoute{}, fmt.Errorf("winnet: GetIpForwardTable2: %d", r1)
	}
	defer procFreeMibTable.Call(uintptr(unsafe.Pointer(table)))

	best := DefaultRoute{}
	bestMetric := ^uint32(0)
	n := table.NumEntries
	rows := unsafe.Slice(&table.Table[0], n)
	for i := 0; i < int(n); i++ {
		row := &rows[i]
		dst, plen, ok := parsePrefix(&row.DestinationPrefix.Prefix, row.DestinationPrefix.PrefixLength)
		if !ok || !dst.Is4() || plen != 0 || !dst.IsUnspecified() {
			continue
		}
		nh, ok := parseAddr(&row.NextHop)
		if !ok || !nh.Is4() {
			continue
		}
		if row.Metric < bestMetric {
			bestMetric = row.Metric
			best = DefaultRoute{
				Present:        true,
				NextHop:        nh,
				InterfaceIndex: row.InterfaceIndex,
				InterfaceLUID:  row.InterfaceLuid,
				Metric:         row.Metric,
			}
		}
	}
	return best, nil
}


// AddRoute creates an IPv4 forward entry via CreateIpForwardEntry2.
// ValidLifetime/PreferredLifetime must be 0xffffffff (infinite); zero lifetime
// yields routes that vanish immediately and break full-tunnel VPN.
func AddRoute(spec RouteSpec) error {
	const infinite uint32 = 0xffffffff
	row := mibIpForwardRow2{
		InterfaceLuid:    spec.InterfaceLUID,
		InterfaceIndex:   spec.InterfaceIndex,
		Metric:           spec.Metric,
		ValidLifetime:     infinite,
		PreferredLifetime: infinite,
		Protocol:         3, // MIB_IPPROTO_NETMGMT
		Origin:           4, // NlroManual
	}
	if err := putPrefix(&row.DestinationPrefix.Prefix, &row.DestinationPrefix.PrefixLength, spec.Destination); err != nil {
		return err
	}
	if err := putAddr(&row.NextHop, spec.NextHop); err != nil {
		return err
	}
	r1, _, e1 := procCreateIpForwardEntry2.Call(uintptr(unsafe.Pointer(&row)))
	if r1 != 0 {
		return fmt.Errorf("winnet: CreateIpForwardEntry2: %v (%w)", r1, e1)
	}
	return nil
}

// HasIPv4Route reports whether a matching forward entry exists on the requested interface.
func HasIPv4Route(spec RouteSpec) (bool, error) {
	var table *mibIpForwardTable2
	r1, _, e1 := procGetIpForwardTable2.Call(uintptr(afINET), uintptr(unsafe.Pointer(&table)))
	if r1 != 0 {
		if e1 != windows.ERROR_SUCCESS {
			return false, fmt.Errorf("winnet: GetIpForwardTable2: %w", e1)
		}
		return false, fmt.Errorf("winnet: GetIpForwardTable2: %d", r1)
	}
	defer procFreeMibTable.Call(uintptr(unsafe.Pointer(table)))
	n := table.NumEntries
	rows := unsafe.Slice(&table.Table[0], n)
	wantBits := spec.Destination.Bits()
	wantAddr := spec.Destination.Addr()
	for i := 0; i < int(n); i++ {
		row := &rows[i]
		dst, plen, ok := parsePrefix(&row.DestinationPrefix.Prefix, row.DestinationPrefix.PrefixLength)
		if !ok || plen != wantBits || dst != wantAddr {
			continue
		}
		nh, ok := parseAddr(&row.NextHop)
		if !ok {
			continue
		}
		if nh != spec.NextHop {
			continue
		}
		// Fail-closed: when caller binds to a TUN interface, require that binding.
		if spec.InterfaceIndex != 0 && row.InterfaceIndex != spec.InterfaceIndex {
			continue
		}
		if spec.InterfaceLUID != 0 && row.InterfaceLuid != spec.InterfaceLUID {
			continue
		}
		// Reject already-expired / zero-lifetime entries (pre-1.0.3 AddRoute bug).
		if row.ValidLifetime == 0 {
			continue
		}
		return true, nil
	}
	return false, nil
}

// DeleteRoute removes a matching IPv4 forward entry.
func DeleteRoute(spec RouteSpec) error {
	row := mibIpForwardRow2{
		InterfaceLuid:  spec.InterfaceLUID,
		InterfaceIndex: spec.InterfaceIndex,
	}
	if err := putPrefix(&row.DestinationPrefix.Prefix, &row.DestinationPrefix.PrefixLength, spec.Destination); err != nil {
		return err
	}
	if err := putAddr(&row.NextHop, spec.NextHop); err != nil {
		return err
	}
	r1, _, e1 := procDeleteIpForwardEntry2.Call(uintptr(unsafe.Pointer(&row)))
	if r1 != 0 {
		return fmt.Errorf("winnet: DeleteIpForwardEntry2: %v (%w)", r1, e1)
	}
	return nil
}

func parsePrefix(raw *[28]byte, plen uint8) (netip.Addr, int, bool) {
	family := binary.LittleEndian.Uint16(raw[0:2])
	if family == afINET {
		var a [4]byte
		copy(a[:], raw[4:8])
		addr := netip.AddrFrom4(a)
		return addr, int(plen), true
	}
	return netip.Addr{}, 0, false
}

func parseAddr(raw *[28]byte) (netip.Addr, bool) {
	family := binary.LittleEndian.Uint16(raw[0:2])
	if family == afINET {
		var a [4]byte
		copy(a[:], raw[4:8])
		return netip.AddrFrom4(a), true
	}
	return netip.Addr{}, false
}

func putPrefix(raw *[28]byte, plen *uint8, pfx netip.Prefix) error {
	if !pfx.Addr().Is4() {
		return fmt.Errorf("winnet: IPv4 prefix required")
	}
	*plen = uint8(pfx.Bits())
	binary.LittleEndian.PutUint16(raw[0:2], afINET)
	a := pfx.Addr().As4()
	copy(raw[4:8], a[:])
	return nil
}

func putAddr(raw *[28]byte, addr netip.Addr) error {
	if !addr.Is4() {
		return fmt.Errorf("winnet: IPv4 next hop required")
	}
	binary.LittleEndian.PutUint16(raw[0:2], afINET)
	a := addr.As4()
	copy(raw[4:8], a[:])
	return nil
}
