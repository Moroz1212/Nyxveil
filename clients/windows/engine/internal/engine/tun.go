package engine

import (
	"context"
	"errors"
	"fmt"

	nvpwin "github.com/nyxveil/nvp/core/platform/windows"
	"github.com/nyxveil/nvp/core/tunnel"
)

// ErrNotLinked is returned when the Wintun platform binding is not available.
// Alias of Frozen Core windows.ErrWintunNotLinked.
var ErrNotLinked = nvpwin.ErrWintunNotLinked

// TunFactory opens the Windows TUN device via Frozen Core Factory + OpenFunc inject.
type TunFactory struct {
	inner *nvpwin.Factory
}

// Open creates the TUN adapter. Without OpenFunc / Wintun DLL bind → ErrNotLinked.
func (f *TunFactory) Open(ctx context.Context, cfg tunnel.Config) (tunnel.Device, error) {
	if f == nil || f.inner == nil {
		return nil, fmt.Errorf("%w", ErrNotLinked)
	}
	dev, err := f.inner.Open(ctx, cfg)
	if err != nil {
		if errors.Is(err, nvpwin.ErrWintunNotLinked) {
			return nil, fmt.Errorf("%w", ErrNotLinked)
		}
		return nil, err
	}
	return dev, nil
}
