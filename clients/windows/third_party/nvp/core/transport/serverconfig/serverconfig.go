package serverconfig

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/ech"
	quictransport "github.com/nyxveil/nvp/core/transport/quic"
	"github.com/nyxveil/nvp/core/transport/tlsstream"
)

// TLSServerConfig builds a TLS 1.3 server listener with optional ECH keys.
type TLSServerConfig struct {
	Cert       tls.Certificate
	ECHKeys    *ech.KeySet
	RequireECH bool
}

// QUICServerConfig builds a QUIC/HTTP3 server listener with optional ECH keys.
type QUICServerConfig struct {
	Cert       tls.Certificate
	ECHKeys    *ech.KeySet
	RequireECH bool
}

// TLSConfig returns a *tls.Config. When ECHKeys is set, ApplyServerKeys installs
// the current EncryptedClientHelloKeys snapshot at listener build time.
// Live mid-connection ECH key rotation via GetEncryptedClientHelloKeys is not
// used in Core 1.0 — rotate by rebuilding/reconfiguring the listener with an
// updated KeySet (atomic config/listener update).
func (c TLSServerConfig) TLSConfig() *tls.Config {
	return buildTLS(c.Cert, c.ECHKeys)
}

// TLSConfig returns a *tls.Config for QUIC/HTTP3 (ALPN h3).
func (c QUICServerConfig) TLSConfig() *tls.Config {
	cfg := buildTLS(c.Cert, c.ECHKeys)
	cfg.NextProtos = []string{"h3"}
	return cfg
}

func buildTLS(cert tls.Certificate, keys *ech.KeySet) *tls.Config {
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	if keys != nil {
		// Always install current keys (Go 1.24 EncryptedClientHelloKeys).
		ech.ApplyServerKeys(cfg, keys.Keys())
	}
	return cfg
}

// NewTLSListener creates a TLS stream listener that always applies ECH keys when provided.
func NewTLSListener(ctx context.Context, addr string, cfg TLSServerConfig) (transport.Listener, error) {
	tlsCfg := cfg.TLSConfig()
	t := tlsstream.NewTransport()
	ln, err := t.Listen(ctx, addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	if !cfg.RequireECH {
		return ln, nil
	}
	return &requireECHListener{inner: ln}, nil
}

// ListenQUIC creates a QUIC/HTTP3 listener that always applies ECH keys when provided.
func ListenQUIC(ctx context.Context, addr string, cfg QUICServerConfig) (transport.Listener, error) {
	tlsCfg := cfg.TLSConfig()
	t := quictransport.NewTransport()
	ln, err := t.Listen(ctx, addr, tlsCfg)
	if err != nil {
		return nil, err
	}
	if !cfg.RequireECH {
		return ln, nil
	}
	return &requireECHListener{inner: ln}, nil
}

// requireECHListener rejects accepted connections that did not negotiate ECH.
type requireECHListener struct {
	inner transport.Listener
}

func (l *requireECHListener) Accept(ctx context.Context) (transport.Conn, error) {
	for {
		c, err := l.inner.Accept(ctx)
		if err != nil {
			return nil, err
		}
		if st, ok := c.(interface{ ConnectionState() tls.ConnectionState }); ok {
			if !st.ConnectionState().ECHAccepted {
				_ = c.Close()
				continue
			}
		}
		return c, nil
	}
}

func (l *requireECHListener) Close() error   { return l.inner.Close() }
func (l *requireECHListener) Addr() net.Addr { return l.inner.Addr() }
