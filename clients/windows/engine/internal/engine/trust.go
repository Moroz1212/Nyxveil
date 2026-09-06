package engine

import (
	"crypto/x509"
	"fmt"
	"os"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/transport"
)

// TrustProvider supplies TLS dial params for Frozen failover.
// RootCAs: nil → system trust store; non-nil → optional private CA pool.
// Never sets InsecureSkipVerify (Frozen transports do not expose that flag).
type TrustProvider struct {
	rootCAs *x509.CertPool
}

// NewSystemTrust uses the OS certificate store (RootCAs=nil).
func NewSystemTrust() *TrustProvider {
	return &TrustProvider{}
}

// NewTrustWithCAFile loads a PEM CA file into RootCAs (private CA path).
func NewTrustWithCAFile(path string) (*TrustProvider, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("engine: read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("engine: no PEM certificates in %s", path)
	}
	return &TrustProvider{rootCAs: pool}, nil
}

func (p *TrustProvider) RootCAs() interface{} {
	if p == nil || p.rootCAs == nil {
		return nil
	}
	return p.rootCAs
}

func (p *TrustProvider) ServerNameFor(node model.NodeRegistryEntry) string {
	return node.ServerName
}

// PinnedPubKeyFor returns nil so failover uses catalog node.SPKIPin.
func (p *TrustProvider) PinnedPubKeyFor(model.NodeRegistryEntry) []byte { return nil }

func (p *TrustProvider) ECHPolicy() transport.ECHPolicy { return "" }
func (p *TrustProvider) ECHConfigList() []byte          { return nil }
