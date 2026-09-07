//go:build windows

package engine

import (
	"fmt"

	"github.com/nyxveil/client-windows/internal/wintundev"
	"github.com/nyxveil/client-windows/internal/winnet"
	"github.com/nyxveil/nvp/core/tunnel"
)

// bindTunnelIdentity records Wintun LUID/ifIndex after a real CreateAdapter.
// Injected test TUNs (non-*wintundev.device) skip OS lookup; VerifyTunnel +
// WindowsApplier still refuse Connected without a confirmed apply.
func bindTunnelIdentity(plan *Plan, tunDev tunnel.Device) error {
	if plan == nil || tunDev == nil {
		return fmt.Errorf("engine: bind tunnel: nil plan/device")
	}
	if luid, ok := wintundev.AdapterLUID(tunDev); ok {
		plan.TunLUID = luid
		name := plan.TunName
		if name == "" {
			name = tunDev.Name()
		}
		idx, err := winnet.InterfaceIndexByAlias(name)
		if err != nil || idx == 0 {
			return fmt.Errorf("engine: Wintun adapter %q not visible after Open: %w", name, err)
		}
		plan.TunIfIndex = idx
		plan.Gate.AdapterOpen = true
		return nil
	}
	// Fake TUN for unit tests — adapter presence is not claimed on the OS.
	plan.Gate.AdapterOpen = true
	return nil
}
