package nvperr_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nyxveil/nvp/core/nvperr"
)

func TestSentinelsAreDistinctAndIsFriendly(t *testing.T) {
	all := []error{
		nvperr.ErrLicenseInvalid,
		nvperr.ErrLicenseExpired,
		nvperr.ErrDeviceUnauthorized,
		nvperr.ErrTicketRejected,
		nvperr.ErrLocationNotAllowed,
		nvperr.ErrNoHealthyNodes,
		nvperr.ErrTransportUnavailable,
		nvperr.ErrServerIdentityMismatch,
		nvperr.ErrECHRequiredUnavailable,
		nvperr.ErrAuthTimeout,
		nvperr.ErrHandshakeFailed,
		nvperr.ErrSessionClosed,
		nvperr.ErrAuthFailed,
		nvperr.ErrDeviceKeyRequired,
	}
	for i, a := range all {
		wrapped := fmt.Errorf("wrap: %w", a)
		if !errors.Is(wrapped, a) {
			t.Fatalf("%v not errors.Is friendly", a)
		}
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Fatalf("%v should not match %v", a, b)
			}
		}
	}
}
