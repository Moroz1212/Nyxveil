package protectnet

import (
	"fmt"
	"syscall"
)

// ProtectFunc protects a socket FD via VpnService.protect (Android).
type ProtectFunc func(fd int) bool

func Control(protect ProtectFunc) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		_ = network
		_ = address
		if protect == nil {
			return fmt.Errorf("protectnet: protector not set")
		}
		var opErr error
		if err := c.Control(func(fd uintptr) {
			if !protect(int(fd)) {
				opErr = fmt.Errorf("protectnet: VpnService.protect(%d) failed", fd)
			}
		}); err != nil {
			return err
		}
		return opErr
	}
}
