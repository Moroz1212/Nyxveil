// Package tunnel defines platform-agnostic TUN device abstraction.
// Split tunneling and per-app routing are handled at platform layer.
package tunnel

import (
	"context"
	"io"
)

// Device represents a platform TUN interface for IP packets.
type Device interface {
	io.ReadWriteCloser
	MTU() int
	Name() string
}

// Config holds TUN device configuration.
type Config struct {
	Name string
	MTU  int
}

// Factory creates platform TUN devices.
type Factory interface {
	Open(ctx context.Context, cfg Config) (Device, error)
}

// RouteMode describes client routing policy (platform layer).
type RouteMode int

const (
	RouteAll RouteMode = iota
	RouteSelectedApps
	RouteOff
)
