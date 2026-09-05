package transport

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Profile identifies a transport configuration.
type Profile string

const (
	ProfileQUICUDP Profile = "quic-udp-443"
	ProfileTLSTCP  Profile = "tls-tcp-443"
)

// Application protocol negotiation:
//   - TLS/TCP: no application ALPN marker. NextProtos stays empty; NVP version
//     negotiation happens inside the encrypted session handshake.
//   - QUIC/UDP: real HTTP/3 (ALPN h3) with CONNECT tunnel carrying NVP frames.
// Never set NextProtos to a custom VPN marker (nvp family / product name / generic vpn).
// Never claim h2/h3 without a real HTTP/2 or HTTP/3 stack.

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
	PinnedPubKey  []byte      // required SPKI pin from signed node descriptor when set
	Timeout       time.Duration
}

// Conn is an abstract bidirectional transport connection.
// Control and DATA may share the same path; DatagramConn optionally separates DATA.
type Conn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
	Close() error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Profile() Profile
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// DatagramConn optionally carries unreliable VPN DATA (QUIC DATAGRAM).
type DatagramConn interface {
	Conn
	DatagramsEnabled() bool
	WriteDatagram(ctx context.Context, data []byte) error
	ReadDatagram(ctx context.Context) ([]byte, error)
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

// Registry holds available transports by profile.
type Registry struct {
	mu         sync.RWMutex
	transports map[Profile]Transport
}

// NewRegistry creates an empty transport registry.
func NewRegistry() *Registry {
	return &Registry{transports: make(map[Profile]Transport)}
}

// Register adds a transport implementation.
func (r *Registry) Register(t Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transports[t.Profile()] = t
}

// Get returns transport for profile.
func (r *Registry) Get(profile Profile) (Transport, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.transports[profile]
	return t, ok
}

// DialWithRacing starts primary immediately and fallback after FallbackDelay.
// The first successful dial wins; losers are closed. Does not wait for a slow
// primary after fallback already succeeded. Primary failure starts fallback immediately.
func (r *Registry) DialWithRacing(ctx context.Context, cfg DialConfig, racing RacingConfig) (Conn, error) {
	type attempt struct {
		conn Conn
		err  error
	}
	results := make(chan attempt, 2)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	launch := func(profile Profile) {
		t, ok := r.Get(profile)
		if !ok {
			results <- attempt{err: fmt.Errorf("transport not registered: %s", profile)}
			return
		}
		dcfg := cfg
		dcfg.Profile = profile
		conn, err := t.Dial(ctx, dcfg)
		select {
		case results <- attempt{conn: conn, err: err}:
		case <-ctx.Done():
			if err == nil && conn != nil {
				_ = conn.Close()
			}
		}
	}

	go launch(racing.Primary)
	inFlight := 1
	fallbackStarted := false
	var lastErr error

	delay := racing.FallbackDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	startFallback := func() {
		if fallbackStarted || racing.Fallback == "" || racing.Fallback == racing.Primary {
			return
		}
		fallbackStarted = true
		timer.Stop()
		inFlight++
		go launch(racing.Fallback)
	}

	for inFlight > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			startFallback()
		case res := <-results:
			inFlight--
			if res.err != nil {
				lastErr = res.err
				startFallback()
				continue
			}
			cancel()
			go func(keep Conn, remaining int) {
				for remaining > 0 {
					r2 := <-results
					remaining--
					if r2.err == nil && r2.conn != nil && r2.conn != keep {
						_ = r2.conn.Close()
					}
				}
			}(res.conn, inFlight)
			return res.conn, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all transports failed")
	}
	return nil, lastErr
}
