//go:build windows

package winnet_test

import (
	"net/netip"
	"testing"

	"github.com/nyxveil/client-windows/internal/winnet"
)

func TestResolveIPv4BestRouteReturnsBestSource(t *testing.T) {
	br, err := winnet.ResolveIPv4BestRoute(netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Skipf("GetBestRoute2: %v", err)
	}
	if !br.Present {
		t.Fatal("expected present")
	}
	if !br.BestSource.IsValid() || !br.BestSource.Is4() {
		t.Fatalf("BestSource missing: %+v", br)
	}
}
