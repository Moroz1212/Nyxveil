package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	quictransport "github.com/nyxveil/nvp/core/transport/quic"
	tlsstream "github.com/nyxveil/nvp/core/transport/tlsstream"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4433", "server host:port")
	transportName := flag.String("transport", "auto", "transport: auto|quic|tls")
	ticketJWT := flag.String("ticket", "", "access ticket JWT (never printed)")
	deviceKey := flag.String("device-key", "", "PEM Ed25519 device private key for AUTH")
	caFile := flag.String("ca", "", "CA PEM for TLS verification")
	serverName := flag.String("server-name", "localhost", "TLS server name")
	flag.Parse()

	host, portStr, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("port: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	reg := transport.NewRegistry()
	reg.Register(quictransport.NewTransport())
	reg.Register(tlsstream.NewTransport())

	cfg := transport.DialConfig{
		Endpoint:   transport.Endpoint{Host: host, Port: port},
		ServerName: *serverName,
		Timeout:    10 * time.Second,
	}
	if *caFile != "" {
		pemBytes, err := os.ReadFile(*caFile)
		if err != nil {
			log.Fatal(err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			log.Fatal("no certificates in -ca file")
		}
		cfg.RootCAs = pool
	}

	var conn transport.Conn
	switch *transportName {
	case "quic":
		conn, err = quictransport.NewTransport().Dial(ctx, cfg)
	case "tls":
		conn, err = tlsstream.NewTransport().Dial(ctx, cfg)
	case "auto":
		conn, err = reg.DialWithRacing(ctx, cfg, transport.DefaultRacingConfig())
	default:
		log.Fatalf("unknown -transport %q", *transportName)
	}
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	fmt.Printf("connected via %s to %s\n", conn.Profile(), *addr)

	clientSess := session.New(session.DefaultConfig(true))
	if err := clientSess.Connect(ctx, conn); err != nil {
		log.Fatalf("connect: %v", err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		log.Fatalf("handshake: %v", err)
	}
	fmt.Printf("handshake ok state=%s\n", clientSess.State())

	go func() { _ = clientSess.ReadLoop(ctx) }()

	if *ticketJWT != "" && *deviceKey != "" {
		priv, err := loadDeviceKey(*deviceKey)
		if err != nil {
			log.Fatalf("device-key: %v", err)
		}
		body, err := ticket.EncodeAuthPayload(*ticketJWT, clientSess.Transcript(), priv)
		if err != nil {
			log.Fatalf("auth encode: %v", err)
		}
		if err := clientSess.SendAuth(ctx, body); err != nil {
			log.Fatalf("auth: %v", err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if clientSess.State() == session.StateEstablished {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	} else if *ticketJWT != "" {
		log.Print("warning: -ticket set without -device-key; AUTH skipped")
	}

	fmt.Printf("client state: %s\n", clientSess.State())
}

func loadDeviceKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not ed25519 private key")
	}
	return priv, nil
}
