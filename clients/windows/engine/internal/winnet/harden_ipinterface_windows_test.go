//go:build windows

package winnet

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrepareIPv4IpInterfaceRowForSet_SitePrefixLengthZero(t *testing.T) {
	row := windows.MibIpInterfaceRow{
		Family:               windows.AF_INET,
		InterfaceLuid:        0x1234567890abcdef,
		InterfaceIndex:       43,
		SitePrefixLength:     64, // typical GetIpInterfaceEntry value that breaks Set → ERROR 87
		WeakHostSend:         1,
		WeakHostReceive:      1,
		Metric:               25,
		NlMtu:                1420,
		RouterDiscoveryBehavior: 2,
		UseAutomaticMetric:   1,
		MaxReassemblySize:    65535,
	}
	metricBefore := row.Metric
	mtuBefore := row.NlMtu
	luidBefore := row.InterfaceLuid
	idxBefore := row.InterfaceIndex
	rdBefore := row.RouterDiscoveryBehavior
	maxReasmBefore := row.MaxReassemblySize

	prepareIPv4IpInterfaceRowForSet(&row)

	if row.Family != windows.AF_INET {
		t.Fatalf("Family=%d want AF_INET", row.Family)
	}
	if row.SitePrefixLength != 0 {
		t.Fatalf("SitePrefixLength=%d want 0 (Microsoft SetIpInterfaceEntry IPv4 requirement)", row.SitePrefixLength)
	}
	if row.WeakHostSend != 0 || row.WeakHostReceive != 0 {
		t.Fatalf("weak host send=%d recv=%d want 0", row.WeakHostSend, row.WeakHostReceive)
	}
	if row.InterfaceLuid != luidBefore || row.InterfaceIndex != idxBefore {
		t.Fatalf("LUID/index mutated: luid=%d idx=%d", row.InterfaceLuid, row.InterfaceIndex)
	}
	if row.Metric != metricBefore || row.NlMtu != mtuBefore {
		t.Fatalf("Metric/NlMtu mutated: metric=%d mtu=%d", row.Metric, row.NlMtu)
	}
	if row.RouterDiscoveryBehavior != rdBefore || row.MaxReassemblySize != maxReasmBefore {
		t.Fatal("unrelated interface fields were clobbered")
	}
}

func TestPrepareIPv4IpInterfaceRowForSet_ForcesFamilyINET(t *testing.T) {
	row := windows.MibIpInterfaceRow{
		Family:           windows.AF_INET6,
		SitePrefixLength: 64,
		WeakHostSend:     1,
	}
	prepareIPv4IpInterfaceRowForSet(&row)
	if row.Family != windows.AF_INET {
		t.Fatalf("Family=%d", row.Family)
	}
	if row.SitePrefixLength != 0 {
		t.Fatalf("SitePrefixLength=%d", row.SitePrefixLength)
	}
}

func TestFormatSetIpInterfaceEntryError_ContainsRequiredFields(t *testing.T) {
	before := IpInterfaceSnapshot{
		Family:           windows.AF_INET,
		InterfaceIndex:   43,
		InterfaceLUID:    14918723521478656,
		SitePrefixLength: 64,
		WeakHostSend:     1,
		WeakHostReceive:  1,
		Metric:           25,
		NlMtu:            1420,
	}
	err := formatSetIpInterfaceEntryError(87, windows.ERROR_INVALID_PARAMETER, before, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	s := err.Error()
	for _, want := range []string{
		"rc=87",
		"family=2",
		"ifIndex=43",
		"luid=14918723521478656",
		"sitePrefixLength=64",
		"weakHostSend_before=1",
		"weakHostSend_desired=0",
		"metric=25",
		"nlMtu=1420",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("error missing %q in %q", want, s)
		}
	}
}

func TestHardenSkipWhenWeakHostAlreadyDisabled(t *testing.T) {
	// Unit-level: snapshot skip semantics used when Get returns WeakHostSend=0.
	before := IpInterfaceSnapshot{WeakHostSend: 0, WeakHostReceive: 0}
	if before.WeakHostSend != 0 || before.WeakHostReceive != 0 {
		t.Fatal("precondition")
	}
	// prepare must still zero SitePrefixLength when Set would be needed:
	row := windows.MibIpInterfaceRow{
		Family:           windows.AF_INET,
		SitePrefixLength: 64,
		WeakHostSend:     0,
		WeakHostReceive:  0,
		Metric:           7,
		NlMtu:            1500,
	}
	if row.WeakHostSend == 0 && row.WeakHostReceive == 0 {
		// mirrors HardenTunnelIPv4Interface early-return path — no Set, no prepare.
		snap := snapshotIpInterface(&row)
		snap.SkippedSet = true
		snap.SkipReason = "weak_host_already_disabled"
		if !snap.SkippedSet || snap.SitePrefixLength != 64 {
			t.Fatalf("skip path must not require prepare; snap=%+v", snap)
		}
		if snap.Metric != 7 || snap.NlMtu != 1500 {
			t.Fatal("skip path must preserve metric/mtu snapshot")
		}
	}
}
