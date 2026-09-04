//go:build !windows

package windows

import (
	"context"
	"fmt"

	"github.com/nyxveil/nvp/tunnel"
)

// Factory stub on non-Windows platforms.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	_ = ctx
	_ = cfg
	return nil, fmt.Errorf("windows tun only available on windows")
}
