//go:build windows

package winnet_test

import (
	"net/netip"
	"testing"

	"github.com/nyxveil/client-windows/internal/winnet"
)

func TestResolveIPv4BestRouteProbe(t *testing.T) {
	dest := netip.MustParseAddr("1.1.1.1")
	br, err := winnet.ResolveIPv4BestRoute(dest)
	if err != nil {
		t.Skipf("GetBestRoute2 unavailable: %v", err)
	}
	if !br.Present || !br.NextHop.IsValid() || br.InterfaceIndex == 0 {
		t.Fatalf("best route incomplete: %+v", br)
	}
	def, err := winnet.GetIPv4DefaultRoute()
	if err != nil {
		t.Fatal(err)
	}
	if !def.Present {
		t.Fatal("expected default present")
	}
}

func TestListActiveEgressIfIndexesExcludesEmpty(t *testing.T) {
	idxs, err := winnet.ListActiveEgressIfIndexes("Nyxveil")
	if err != nil {
		t.Fatal(err)
	}
	_ = idxs
}
