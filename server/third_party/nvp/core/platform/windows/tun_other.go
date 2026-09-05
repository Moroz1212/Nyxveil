//go:build !windows

package windows

import (
	"context"
	"fmt"

	"github.com/nyxveil/nvp/core/tunnel"
)

// Factory is unavailable on non-Windows builds.
type Factory struct{}

func NewFactory() *Factory { return &Factory{} }

func (f *Factory) Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	_ = ctx
	_ = cfg
	return nil, fmt.Errorf("%w", ErrWintunNotLinked)
}
