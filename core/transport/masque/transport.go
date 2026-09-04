package masque

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/nyxveil/nvp/transport"
)

var ErrNotImplemented = errors.New("masque connect-udp transport: not implemented in v1 (interface reserved)")

// Transport implements standards-compliant CONNECT-UDP / MASQUE profile (stub for v1).
// Full HTTP/3 CONNECT-UDP will be added without changing session core.
type Transport struct{}

func NewTransport() *Transport { return &Transport{} }

func (t *Transport) Profile() transport.Profile { return transport.ProfileMASQUE }

func (t *Transport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	_ = ctx
	_ = cfg
	return nil, fmt.Errorf("%w: target %s:%d", ErrNotImplemented, cfg.Endpoint.Host, cfg.Endpoint.Port)
}

func (t *Transport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	_ = ctx
	_ = tlsConfig
	return nil, fmt.Errorf("%w: listen on %s", ErrNotImplemented, addr)
}

// Registerable reports whether MASQUE is available in this build.
func Registerable() bool { return false }

// Capabilities describes MASQUE transport requirements for future implementation.
type Capabilities struct {
	RequiresHTTP3  bool
	RequiresTLS13  bool
	Method         string
	TargetTemplate string
}

// DefaultCapabilities returns planned MASQUE profile.
func DefaultCapabilities() Capabilities {
	return Capabilities{
		RequiresHTTP3:  true,
		RequiresTLS13:  true,
		Method:         "CONNECT-UDP",
		TargetTemplate: ":authority",
	}
}

// Ensure Transport satisfies interface at compile time.
var _ transport.Transport = (*Transport)(nil)

// noop listener placeholder
type noopListener struct{ addr net.Addr }

func (n *noopListener) Accept(ctx context.Context) (transport.Conn, error) {
	return nil, ErrNotImplemented
}
func (n *noopListener) Close() error   { return nil }
func (n *noopListener) Addr() net.Addr { return n.addr }
