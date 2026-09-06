package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestRollbackHealthWaitsForControlSocket(t *testing.T) {
	oldActive := serviceActive
	oldSock := controlSocketReady
	oldHealth := ctlHealthJSON
	defer func() {
		serviceActive = oldActive
		controlSocketReady = oldSock
		ctlHealthJSON = oldHealth
	}()

	serviceActive = func(string) bool { return true }
	var sockReady atomic.Bool
	controlSocketReady = func() bool { return sockReady.Load() }
	ctlHealthJSON = func() ([]byte, error) { return []byte(`{"healthy":true}`), nil }

	done := make(chan bool, 1)
	go func() {
		done <- verifyServiceHealth(5)
	}()
	time.Sleep(1500 * time.Millisecond)
	sockReady.Store(true)
	ok := <-done
	if !ok {
		t.Fatal("expected health pass after socket appears")
	}
}

func TestRollbackDoesNotReportSuccessUntilHealthPasses(t *testing.T) {
	oldActive := serviceActive
	oldSock := controlSocketReady
	oldHealth := ctlHealthJSON
	defer func() {
		serviceActive = oldActive
		controlSocketReady = oldSock
		ctlHealthJSON = oldHealth
	}()
	serviceActive = func(string) bool { return true }
	controlSocketReady = func() bool { return true }
	ctlHealthJSON = func() ([]byte, error) { return []byte(`{"healthy":false}`), nil }
	if verifyServiceHealth(4) {
		t.Fatal("must not report healthy while /health is false")
	}
}
