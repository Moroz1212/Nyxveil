//go:build !linux

package tun

import (
	"errors"
	"fmt"
)

// ErrUnsupported is returned on non-Linux platforms (Windows CI, etc.).
var ErrUnsupported = errors.New("tun: unsupported on this platform")

// Device is a stub TUN device for non-Linux builds.
type Device struct{}

// Open returns ErrUnsupported outside Linux.
func Open(name, addrCIDR string, mtu int) (*Device, error) {
	_ = name
	_ = addrCIDR
	_ = mtu
	return nil, fmt.Errorf("%w (linux only)", ErrUnsupported)
}

func (d *Device) Name() string { return "" }
func (d *Device) MTU() int     { return 0 }
func (d *Device) Read(p []byte) (int, error) {
	return 0, ErrUnsupported
}
func (d *Device) Write(p []byte) (int, error) {
	return 0, ErrUnsupported
}
func (d *Device) Close() error { return nil }
