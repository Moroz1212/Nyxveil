package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/authhandler"
	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport"
	"github.com/nyxveil/nvp/core/transport/serverconfig"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:4433", "listen address for TLS and QUIC")
	nodeID := flag.String("node-id", "test-node-01", "node identifier")
	locationID := flag.String("location-id", "fi-hel", "location identifier")
	cpPubKeyFile := flag.String("cp-pubkey", "", "PEM Ed25519 CP public key for ticket verify (optional)")
	certFile := flag.String("cert", "", "TLS certificate PEM (optional; generated if empty)")
	keyFile := flag.String("key", "", "TLS private key PEM (optional; generated if empty)")
	caOut := flag.String("write-ca", "", "optional path to write generated CA PEM for clients")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	tlsCfg, caPEM, err := loadOrGenerateTLS(*certFile, *keyFile)
	if err != nil {
		log.Fatal(err)
	}
	if *caOut != "" && len(caPEM) > 0 {
		if err := os.WriteFile(*caOut, caPEM, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote CA for clients: %s\n", *caOut)
	}

	verifier, err := buildVerifier(*cpPubKeyFile)
	if err != nil {
		log.Fatal(err)
	}

	auth := authhandler.NewAuthHandler(*nodeID, *locationID, verifier)

	quicLn, err := serverconfig.ListenQUIC(ctx, *listen, serverconfig.QUICServerConfig{
		Cert: tlsCfg.Certificates[0],
	})
	if err != nil {
		log.Fatalf("quic listen: %v", err)
	}
	tlsLn, err := serverconfig.NewTLSListener(ctx, *listen, serverconfig.TLSServerConfig{
		Cert: tlsCfg.Certificates[0],
	})
	if err != nil {
		log.Fatalf("tls listen: %v", err)
	}

	fmt.Printf("NVP test server node_id=%s location=%s\n", *nodeID, *locationID)
	fmt.Printf("listening TLS+QUIC on %s (TLS: no ALPN; QUIC: HTTP/3 CONNECT h3)\n", *listen)
	fmt.Printf("CP verify keys loaded: %d\n", len(verifier.PublicKeys))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		serveLoop(ctx, quicLn, auth)
	}()
	go func() {
		defer wg.Done()
		serveLoop(ctx, tlsLn, auth)
	}()

	<-ctx.Done()
	_ = quicLn.Close()
	_ = tlsLn.Close()
	wg.Wait()
	log.Println("shutting down")
}

func serveLoop(ctx context.Context, ln transport.Listener, auth *authhandler.AuthHandler) {
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				continue
			}
		}
		go handleConn(ctx, conn, auth)
	}
}

func handleConn(ctx context.Context, conn transport.Conn, auth *authhandler.AuthHandler) {
	defer conn.Close()
	sess := session.New(session.DefaultConfig(false))
	sess.OnControl(func(msgType byte, payload []byte) error {
		if msgType == control.TypeAuth {
			return auth.HandleAuth(ctx, sess, payload)
		}
		return nil
	})
	if err := sess.Connect(ctx, conn); err != nil {
		return
	}
	if err := sess.RunHandshake(ctx); err != nil {
		return
	}
	_ = sess.ReadLoop(ctx)
}

func buildVerifier(cpPubKeyFile string) (ticket.VerifierConfig, error) {
	keys := map[string]ed25519.PublicKey{}
	if cpPubKeyFile != "" {
		pub, err := loadEd25519Pub(cpPubKeyFile)
		if err != nil {
			return ticket.VerifierConfig{}, err
		}
		keys["cp-key-1"] = pub
	} else {
		pub, _, err := ed25519.GenerateKey(nil)
		if err != nil {
			return ticket.VerifierConfig{}, err
		}
		keys["cp-key-1"] = pub
		log.Print("warning: no -cp-pubkey; using ephemeral verify key (tickets from real CP will fail)")
	}
	return ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: keys,
	}, nil
}

func loadOrGenerateTLS(certFile, keyFile string) (*tls.Config, []byte, error) {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, nil, err
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		}, nil, nil
	}
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		return nil, nil, err
	}
	caPEM := testutil.PEMEncodeCert(bundle.CACert)
	return &tls.Config{
		Certificates: []tls.Certificate{bundle.Cert},
		MinVersion:   tls.VersionTLS13,
	}, caPEM, nil
}

func loadEd25519Pub(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ed, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not ed25519 public key")
	}
	return ed, nil
}
