package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/memory"
)

// failingTransport always fails dial (simulates UDP blocked).
type failingTransport struct {
	profile transport.Profile
}

func (f *failingTransport) Profile() transport.Profile { return f.profile }
func (f *failingTransport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	return nil, errors.New("udp blocked")
}
func (f *failingTransport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	return nil, errors.New("not supported")
}

func TestTransportFailoverQUICtoTLS(t *testing.T) {
	clientConn, serverConn := memory.Pair()

	reg := transport.NewRegistry()
	reg.Register(&failingTransport{profile: transport.ProfileQUICUDP})
	reg.Register(&successTransport{conn: clientConn, profile: transport.ProfileTLSTCP})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cfg := transport.DialConfig{
		Endpoint: transport.Endpoint{Host: "127.0.0.1", Port: 443},
		Timeout:  1 * time.Second,
	}
	racing := transport.RacingConfig{
		Primary:       transport.ProfileQUICUDP,
		Fallback:      transport.ProfileTLSTCP,
		FallbackDelay: 50 * time.Millisecond,
	}

	conn, err := reg.DialWithRacing(ctx, cfg, racing)
	if err != nil {
		t.Fatalf("expected TLS fallback success: %v", err)
	}
	if conn.Profile() != transport.ProfileTLSTCP {
		t.Fatalf("expected TLS profile, got %s", conn.Profile())
	}

	// Verify NVP session works over fallback conn
	clientSess := session.New(session.DefaultConfig(true))
	serverSess := session.New(session.DefaultConfig(false))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
	}()

	if err := clientSess.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
	conn.Close()
}

type successTransport struct {
	conn    transport.Conn
	profile transport.Profile
}

func (s *successTransport) Profile() transport.Profile { return s.profile }
func (s *successTransport) Dial(ctx context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	return s.conn, nil
}
func (s *successTransport) Listen(ctx context.Context, addr string, tlsConfig interface{}) (transport.Listener, error) {
	return nil, errors.New("not supported")
}
