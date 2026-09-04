package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nyxveil/nvp/session"
	"github.com/nyxveil/nvp/transport/memory"
)

func main() {
	sessions := flag.Int("sessions", 100, "number of sessions to establish")
	flag.Parse()

	fmt.Printf("NVP Load Harness\n")
	fmt.Printf("Sessions: %d\n\n", *sessions)

	var okCount atomic.Int64
	var failCount atomic.Int64
	start := time.Now()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)

	for i := 0; i < *sessions; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			clientConn, serverConn := memory.Pair()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			clientSess := session.New(session.DefaultConfig(true))
			serverSess := session.New(session.DefaultConfig(false))

			done := make(chan error, 1)
			go func() {
				_ = serverSess.Connect(ctx, serverConn)
				done <- serverSess.RunHandshake(ctx)
			}()

			if err := clientSess.Connect(ctx, clientConn); err != nil {
				failCount.Add(1)
				return
			}
			if err := clientSess.RunHandshake(ctx); err != nil {
				failCount.Add(1)
				return
			}
			if err := <-done; err != nil {
				failCount.Add(1)
				return
			}
			okCount.Add(1)
			_ = clientSess.Close(ctx)
			_ = serverSess.Close(ctx)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	ok := okCount.Load()
	fail := failCount.Load()

	fmt.Printf("Results:\n")
	fmt.Printf("  Successful handshakes: %d\n", ok)
	fmt.Printf("  Failed handshakes:     %d\n", fail)
	fmt.Printf("  Total time:            %v\n", elapsed)
	if ok > 0 {
		fmt.Printf("  Handshakes/sec:        %.2f\n", float64(ok)/elapsed.Seconds())
	}
	if fail > 0 {
		log.Printf("warning: %d handshakes failed", fail)
	}
}
