//go:build !windows

package wintundev

import (
	"context"
	"fmt"

	"github.com/nyxveil/nvp/core/tunnel"
)

func Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	return nil, fmt.Errorf("wintun: Windows only")
}
