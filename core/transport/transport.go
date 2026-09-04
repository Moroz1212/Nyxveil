package transport

import (
	"context"
	"net"
	"time"
)

// Profile identifies a transport configuration.
type Profile string

const (
	ProfileQUICUDP Profile = "quic-udp-443"
	ProfileTLSTCP  Profile = "tls-tcp-443"
	ProfileMASQUE  Profile = "masque-connect-udp" // future
)

// ECHPolicy controls Encrypted ClientHello behavior.
type ECHPolicy string

const (
	ECHPreferred ECHPolicy = "preferred"
	ECHRequired  ECHPolicy = "required"
)

// Endpoint describes a node network endpoint.
type Endpoint struct {
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Profiles []Profile `json:"profiles"`
	IPFamily string    `json:"ip_family,omitempty"` // "ipv4", "ipv6", "dual"
}

// DialConfig holds client dial configuration.
type DialConfig struct {
	Endpoint      Endpoint
	Profile       Profile
	ECHPolicy     ECHPolicy
	ECHConfigList []byte // from DNS HTTPS record
	ServerName    string
	RootCAs       interface{} // *x509.CertPool in implementations
	PinnedPubKey  []byte      // optional SPKI pin from signed node descriptor
	Timeout       time.Duration
}

// Conn is an abstract bidirectional transport connection.
// NVP session layer uses Read/Write without knowledge of UDP/TCP/QUIC.
type Conn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
	Close() error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Profile() Profile
}

// Transport dials and accepts connections for a specific profile.
type Transport interface {
	Profile() Profile
	Dial(ctx context.Context, cfg DialConfig) (Conn, error)
	Listen(ctx context.Context, addr string, tlsConfig interface{}) (Listener, error)
}

// Listener accepts incoming transport connections.
type Listener interface {
	Accept(ctx context.Context) (Conn, error)
	Close() error
	Addr() net.Addr
}

// RacingConfig controls Happy-Eyeballs-like transport selection.
type RacingConfig struct {
	Primary       Profile
	Fallback      Profile
	FallbackDelay time.Duration
	MaxParallel   int
}

// DefaultRacingConfig returns default transport racing settings.
func DefaultRacingConfig() RacingConfig {
	return RacingConfig{
		Primary:       ProfileQUICUDP,
		Fallback:      ProfileTLSTCP,
		FallbackDelay: 250 * time.Millisecond,
		MaxParallel:   2,
	}
}
