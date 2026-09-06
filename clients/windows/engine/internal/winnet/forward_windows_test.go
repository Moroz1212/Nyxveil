//go:build windows

package winnet_test

import (
	"testing"

	"github.com/nyxveil/client-windows/internal/winnet"
)

func TestGetIPv4DefaultRouteUsesIPHelperNotRoutePrint(t *testing.T) {
	def, err := winnet.GetIPv4DefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	if !def.Present {
		t.Skip("no IPv4 default route on this host")
	}
	if def.InterfaceIndex == 0 {
		t.Fatal("InterfaceIndex is 0 — looks like route.exe address-as-index bug regression")
	}
	if !def.NextHop.IsValid() || !def.NextHop.Is4() {
		t.Fatalf("invalid next hop: %v", def.NextHop)
	}
	if def.InterfaceLUID == 0 {
		t.Fatal("InterfaceLUID missing from IP Helper")
	}
}

func TestIPv6CaptureRestoreNoOpZeroIndex(t *testing.T) {
	st, err := winnet.CaptureIPv6State(0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Captured {
		t.Fatal("ifIndex 0 should not claim capture")
	}
	if err := winnet.RestoreIPv6State(st); err != nil {
		t.Fatal(err)
	}
}
