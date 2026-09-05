//go:build windows

package windows

import (
	"context"
	"fmt"

	"github.com/nyxveil/nvp/core/tunnel"
)

// Factory opens Windows TUN devices via a platform binding.
type Factory struct {
	// OpenFunc may be set by platform code to provide a real Wintun adapter.
	OpenFunc func(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error)
}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	if f != nil && f.OpenFunc != nil {
		return f.OpenFunc(ctx, cfg)
	}
	return nil, fmt.Errorf("%w: inject OpenFunc or build with platform Wintun binding", ErrWintunNotLinked)
}
