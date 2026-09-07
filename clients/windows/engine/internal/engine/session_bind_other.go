//go:build !windows

package engine

import "github.com/nyxveil/nvp/core/tunnel"

func bindTunnelIdentity(plan *Plan, tunDev tunnel.Device) error {
	if plan != nil {
		plan.Gate.AdapterOpen = true
		plan.TunIfIndex = 1
		plan.TunLUID = 1
	}
	_ = tunDev
	return nil
}
