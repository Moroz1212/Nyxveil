//go:build windows

package winnet_test

import (
	"testing"

	"github.com/nyxveil/client-windows/internal/winnet"
)

func TestClearDNSServersMissingAdapterIdempotent(t *testing.T) {
	alias := "Nyxveil-Does-Not-Exist-InstallerGate"
	ok, err := winnet.AdapterExistsByAlias(alias)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("test alias unexpectedly exists")
	}
	if err := winnet.ClearDNSServersOnAlias(alias); err != nil {
		t.Fatalf("missing adapter must be idempotent success: %v", err)
	}
}
