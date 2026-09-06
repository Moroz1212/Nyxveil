package runtime

import (
	"fmt"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/localconfig"
)

// buildControlPlaneTLS is a thin wrapper around the shared controlplane.BuildTLS factory.
// Prefer controlplane.NewClientWithTLS for new call sites.
func buildControlPlaneTLS(cfg *localconfig.File) (*controlplane.TLSResult, error) {
	if cfg == nil {
		return controlplane.BuildTLS(controlplane.TLSOptions{BaseURL: "https://invalid.invalid"})
	}
	res, err := controlplane.BuildTLS(controlplane.TLSOptions{
		BaseURL:      cfg.ControlPlaneURL,
		SPKIPinHex:   cfg.ControlPlaneSPKIPin,
		PinnedCAFile: cfg.PinnedCAFile,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	return res, nil
}
