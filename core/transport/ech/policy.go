package ech

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/nyxveil/nvp/transport"
)

var (
	ErrECHRequiredButMissing = errors.New("ech required but not negotiated")
	ErrECHConfigMissing      = errors.New("ech config list not provided")
)

// ApplyClientConfig configures TLS for ECH per policy.
func ApplyClientConfig(cfg *tls.Config, policy transport.ECHPolicy, echConfigList []byte) error {
	if policy == "" {
		policy = transport.ECHPreferred
	}
	switch policy {
	case transport.ECHRequired:
		if len(echConfigList) == 0 {
			return ErrECHConfigMissing
		}
		cfg.EncryptedClientHelloConfigList = echConfigList
	case transport.ECHPreferred:
		if len(echConfigList) > 0 {
			cfg.EncryptedClientHelloConfigList = echConfigList
		}
	}
	return nil
}

// VerifyNegotiated checks whether ECH policy is satisfied after handshake.
func VerifyNegotiated(policy transport.ECHPolicy, state tls.ConnectionState) error {
	if policy == "" {
		policy = transport.ECHPreferred
	}
	if policy != transport.ECHRequired {
		return nil
	}
	if !state.ECHAccepted {
		return ErrECHRequiredButMissing
	}
	return nil
}

// DescribeState returns human-readable ECH status for diagnostics.
func DescribeState(state tls.ConnectionState) string {
	if state.ECHAccepted {
		return "accepted"
	}
	return "not_accepted"
}

// PolicyHint returns guidance when ECH required fails.
func PolicyHint(policy transport.ECHPolicy, hasConfig bool) string {
	switch policy {
	case transport.ECHRequired:
		if !hasConfig {
			return "ECH required but no EncryptedClientHelloConfigList configured (need DNS HTTPS record)"
		}
		return "ECH required but server did not accept ECH"
	default:
		return fmt.Sprintf("ECH policy %s", policy)
	}
}
