package ech

import (
	"crypto/tls"
	"errors"
	"fmt"
	"sync"

	"github.com/nyxveil/nvp/core/transport"
)

var (
	ErrECHRequiredButMissing = errors.New("ech required but not negotiated")
	ErrECHConfigMissing      = errors.New("ech config list not provided")
)

// ApplyClientConfig configures TLS for ECH per policy.
// ECHRequired with an empty config list fails before dial.
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

// ApplyServerKeys installs Encrypted ClientHello private keys on a server tls.Config.
func ApplyServerKeys(cfg *tls.Config, keys []tls.EncryptedClientHelloKey) {
	if cfg == nil {
		return
	}
	cfg.EncryptedClientHelloKeys = append([]tls.EncryptedClientHelloKey(nil), keys...)
}

// KeySet holds a rotatable list of server ECH keys for Listen configs.
type KeySet struct {
	mu   sync.RWMutex
	keys []tls.EncryptedClientHelloKey
}

// NewKeySet creates a KeySet with the given initial keys.
func NewKeySet(keys []tls.EncryptedClientHelloKey) *KeySet {
	return &KeySet{keys: append([]tls.EncryptedClientHelloKey(nil), keys...)}
}

// Rotate replaces the active key list (callers should overlap old+new during DNS transition).
func (k *KeySet) Rotate(keys []tls.EncryptedClientHelloKey) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append([]tls.EncryptedClientHelloKey(nil), keys...)
}

// Keys returns a copy of the current key list.
func (k *KeySet) Keys() []tls.EncryptedClientHelloKey {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return append([]tls.EncryptedClientHelloKey(nil), k.keys...)
}

// ApplyTo copies the current keys into cfg.EncryptedClientHelloKeys.
func (k *KeySet) ApplyTo(cfg *tls.Config) {
	if cfg == nil {
		return
	}
	ApplyServerKeys(cfg, k.Keys())
}
