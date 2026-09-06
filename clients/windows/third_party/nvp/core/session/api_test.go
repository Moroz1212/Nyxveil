package session_test

import (
	"context"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestSessionPublicAPIStatsDoneWritePacket(t *testing.T) {
	clientConn, serverConn := memory.Pair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := session.New(session.DefaultConfig(true))
	server := session.New(session.DefaultConfig(false))

	go func() {
		_ = server.Connect(ctx, serverConn)
		_ = server.RunHandshake(ctx)
		_ = server.MarkEstablished()
		_ = server.ReadLoop(ctx)
	}()

	if err := client.Connect(ctx, clientConn); err != nil {
		t.Fatal(err)
	}
	if err := client.RunHandshake(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.MarkEstablished(); err != nil {
		t.Fatal(err)
	}

	st := client.Stats()
	if st.State != session.StateEstablished || st.Epoch == 0 {
		t.Fatalf("stats=%+v", st)
	}
	if err := client.WritePacket(ctx, []byte{0x45, 0x00}); err != nil {
		t.Fatal(err)
	}
	st = client.Stats()
	if st.SendPackets == 0 {
		t.Fatal("expected send packet counter")
	}

	done := client.Done()
	select {
	case <-done:
		t.Fatal("Done should not be closed yet")
	default:
	}
	_ = client.Close(context.Background())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Done not closed after Close")
	}
}
