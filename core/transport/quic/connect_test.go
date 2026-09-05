package quictransport_test

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/transport"
	quictransport "github.com/nyxveil/nvp/core/transport/quic"
)

func TestHTTP3ConnectRoundTrip(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	tr := quictransport.NewTransport()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := tr.Listen(ctx, "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	var srvConn transport.Conn
	go func() {
		var err error
		srvConn, err = ln.Accept(ctx)
		if err != nil {
			errCh <- err
			return
		}
		msg, err := srvConn.Read(ctx)
		if err != nil {
			errCh <- err
			return
		}
		errCh <- srvConn.Write(ctx, append([]byte("echo:"), msg...))
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	client, err := tr.Dial(ctx, transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: bundle.ServerName,
		RootCAs:    bundle.CAPool,
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := client.Write(ctx, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "echo:ping" {
		t.Fatalf("got %q", resp)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if srvConn != nil {
		_ = srvConn.Close()
	}
}
