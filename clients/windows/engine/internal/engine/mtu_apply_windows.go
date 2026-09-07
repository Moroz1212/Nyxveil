//go:build windows

package engine

import (
	"fmt"
	"strconv"
)

// setTunnelInterfaceMTU sets the IPv4 subinterface MTU via netsh (same mechanism as ApplyTunnel).
func setTunnelInterfaceMTU(name string, mtu int) error {
	if name == "" || mtu <= 0 {
		return fmt.Errorf("routes: invalid MTU apply name=%q mtu=%d", name, mtu)
	}
	return run("netsh", "interface", "ipv4", "set", "subinterface", name, "mtu="+strconv.Itoa(mtu), "store=active")
}
