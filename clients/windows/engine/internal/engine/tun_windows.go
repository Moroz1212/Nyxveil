//go:build windows

package engine

import (
	"context"

	"github.com/nyxveil/client-windows/internal/wintundev"
	nvpwin "github.com/nyxveil/nvp/core/platform/windows"
	"github.com/nyxveil/nvp/core/tunnel"
)

// NewTunFactory wraps Frozen Core platform/windows.Factory with Wintun OpenFunc.
func NewTunFactory() *TunFactory {
	f := nvpwin.NewFactory()
	f.OpenFunc = func(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
		return wintundev.Open(ctx, cfg)
	}
	return &TunFactory{inner: f}
}
