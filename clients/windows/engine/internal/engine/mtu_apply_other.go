//go:build !windows

package engine

func setTunnelInterfaceMTU(name string, mtu int) error {
	_ = name
	_ = mtu
	return nil
}
