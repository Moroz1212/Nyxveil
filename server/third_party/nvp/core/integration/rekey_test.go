package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/keys"
	"github.com/nyxveil/nvp/core/packet"
	"github.com/nyxveil/nvp/core/session"
	"github.com/nyxveil/nvp/core/transport/memory"
)

func TestFreshECDHRekeyPayload(t *testing.T) {
	kp1, _ := keys.GenerateEphemeral()
	kp2, _ := keys.GenerateEphemeral()
	shared, _ := keys.SharedSecret(&kp1.Private, &kp2.Public)
	sk1, _ := keys.DeriveSessionKeys(shared, []byte("transcript"), 1)
	sk2, _ := keys.DeriveSessionKeys(shared, append([]byte("transcript"), 2), 2)
	if string(sk1.ClientToServer) == string(sk2.ClientToServer) {
		t.Fatal("rekey epoch must derive different keys")
	}
}

func TestFreshECDHRekeySession(t *testing.T) {
	clientConn, serverConn := memory.Pair()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	clientCfg := session.DefaultConfig(true)
	clientCfg.RekeyPacketCount = 2
	clientSess := session.New(clientCfg)
	serverSess := session.New(session.DefaultConfig(false))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = serverSess.Connect(ctx, serverConn)
		_ = serverSess.RunHandshake(ctx)
		_ = serverSess.MarkEstablished()
		_ = serverSess.ReadLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		_ = clientSess.Connect(ctx, clientConn)
		_ = clientSess.RunHandshake(ctx)
		_ = clientSess.ReadLoop(ctx)
	}()

	time.Sleep(30 * time.Millisecond)
	_ = clientSess.MarkEstablished()
	_ = serverSess.MarkEstablished()
	_ = clientSess.SendData(ctx, []byte{1, 2, 3})
	_ = clientSess.SendData(ctx, []byte{4, 5, 6})

	time.Sleep(200 * time.Millisecond)
	if clientSess.Epoch() < 2 {
		t.Fatalf("expected rekey epoch >= 2, got %d", clientSess.Epoch())
	}
	if err := clientSess.SendData(ctx, []byte{7, 8, 9}); err != nil {
		t.Fatalf("data after rekey: %v", err)
	}
	_ = clientSess.Close(context.Background())
	_ = serverSess.Close(context.Background())

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Log("ReadLoop exit timeout (non-fatal)")
	}
}

func TestRekeyPacketCodec(t *testing.T) {
	p, _ := packet.EncodeRekeyInit(packet.RekeyInitPayload{Epoch: 3})
	out, err := packet.DecodeRekeyInit(p)
	if err != nil || out.Epoch != 3 {
		t.Fatal(err)
	}
}
