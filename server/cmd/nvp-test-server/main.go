package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/server"
	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport/memory"
)

func main() {
	listen := flag.String("listen", ":4433", "listen address (memory mode ignores)")
	nodeID := flag.String("node-id", "test-node-01", "node identifier")
	cpPubKeyFile := flag.String("cp-pubkey", "", "control plane public key file (optional)")
	flag.Parse()

	_ = listen
	_ = cpPubKeyFile

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Generate ephemeral CP key for demo if not configured
	pub, _, _ := ed25519.GenerateKey(nil)
	verifier := ticket.VerifierConfig{
		Issuer:     "https://control.nyxveil.test",
		Audience:   "nvp-node",
		PublicKeys: map[string]ed25519.PublicKey{"cp-key-1": pub},
	}

	clientConn, serverConn := memory.Pair()
	sess := session.New(session.DefaultConfig(false))
	auth := server.NewAuthHandler(*nodeID, verifier)

	go func() {
		_ = sess.Connect(ctx, serverConn)
		_ = sess.RunHandshake(ctx)
		_ = sess.ReadLoop(ctx)
	}()

	fmt.Printf("NVP test server node_id=%s (memory transport demo)\n", *nodeID)
	fmt.Printf("Control plane pubkey loaded: %d keys\n", len(verifier.PublicKeys))
	_ = clientConn
	_ = auth

	<-ctx.Done()
	log.Println("shutting down")
}
