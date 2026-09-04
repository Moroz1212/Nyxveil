//go:build windows

package windows

import (
	"context"
	"fmt"
	"sync"

	"github.com/nyxveil/nvp/tunnel"
)

// Device is a Windows TUN adapter using Wintun (integration point).
type Device struct {
	mu     sync.Mutex
	name   string
	mtu    int
	closed bool
}

// Factory opens Windows TUN devices.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	if cfg.MTU <= 0 {
		cfg.MTU = 1280
	}
	if cfg.Name == "" {
		cfg.Name = "Nyxveil"
	}
	_ = ctx
	return &Device{name: cfg.Name, mtu: cfg.MTU}, nil
}

func (d *Device) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("tun closed")
	}
	return 0, nil
}

func (d *Device) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, fmt.Errorf("tun closed")
	}
	return len(p), nil
}

func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

func (d *Device) MTU() int     { return d.mtu }
func (d *Device) Name() string { return d.name }
