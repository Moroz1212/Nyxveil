//go:build !windows

package ipc

import (
	"errors"
	"net"
)

func ListenSecure(pipeName string) (net.Listener, error) {
	return nil, errors.New("ipc: named pipe ListenSecure is Windows-only")
}
