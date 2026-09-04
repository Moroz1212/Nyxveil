package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport"
	"github.com/nyxveil/nvp/transport/memory"
)

func main() {
	addr := flag.String("addr", "", "server address (unused in memory test mode)")
	ticket := flag.String("ticket", "", "access ticket JWT")
	flag.Parse()

	_ = addr
	if *ticket == "" {
		log.Print("warning: no ticket provided, auth will fail")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	clientConn, serverConn := memory.Pair()

	serverSess := session.New(session.DefaultConfig(false))
	go runServer(ctx, serverConn, serverSess)

	clientSess := session.New(session.DefaultConfig(true))
	if err := clientSess.Connect(ctx, clientConn); err != nil {
		log.Fatalf("connect: %v", err)
	}
	if err := clientSess.RunHandshake(ctx); err != nil {
		log.Fatalf("handshake: %v", err)
	}
	if *ticket != "" {
		if err := clientSess.SendAuth(ctx, []byte(*ticket)); err != nil {
			log.Fatalf("auth: %v", err)
		}
	}

	time.Sleep(2 * time.Second)
	fmt.Printf("client state: %s\n", clientSess.State())
}

func runServer(ctx context.Context, conn transport.Conn, sess *session.Session) {
	if err := sess.Connect(ctx, conn); err != nil {
		return
	}
	if err := sess.RunHandshake(ctx); err != nil {
		return
	}
	_ = sess.ReadLoop(ctx)
}
