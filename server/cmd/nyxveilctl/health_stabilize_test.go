package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/health"
)

func dataplaneStatusJSON(cpConnected bool) []byte {
	if cpConnected {
		return []byte(`{"running":true,"accepting":true,"bridge_ok":true,"tls_ok":true,"quic_ok":true,"tun_ready":true,"cp_connected":true,"healthy":true,"identity_present":true,"version_blocked":false,"ticket_keys_loaded":true,"revocation_stale":false}`)
	}
	return []byte(`{"running":true,"accepting":true,"bridge_ok":true,"tls_ok":true,"quic_ok":true,"tun_ready":true,"cp_connected":false,"healthy":false,"identity_present":true,"version_blocked":false,"ticket_keys_loaded":false,"revocation_stale":true}`)
}

func TestRollbackHealthWaitsForControlSocket(t *testing.T) {
	oldActive := serviceActive
	oldSock := controlSocketReady
	oldStatus := ctlStatusJSON
	defer func() {
		serviceActive = oldActive
		controlSocketReady = oldSock
		ctlStatusJSON = oldStatus
	}()

	pre := health.CaptureBaseline(mustParseStatus(t, dataplaneStatusJSON(false)))
	serviceActive = func(string) bool { return true }
	var sockReady atomic.Bool
	controlSocketReady = func() bool { return sockReady.Load() }
	ctlStatusJSON = func() ([]byte, error) { return dataplaneStatusJSON(false), nil }

	done := make(chan bool, 1)
	go func() {
		_, ok := verifyRollbackHealth(pre, 5)
		done <- ok
	}()
	time.Sleep(1500 * time.Millisecond)
	sockReady.Store(true)
	ok := <-done
	if !ok {
		t.Fatal("expected rollback health pass after socket appears")
	}
}

func TestRollbackDoesNotReportSuccessUntilDataplanePasses(t *testing.T) {
	oldActive := serviceActive
	oldSock := controlSocketReady
	oldStatus := ctlStatusJSON
	defer func() {
		serviceActive = oldActive
		controlSocketReady = oldSock
		ctlStatusJSON = oldStatus
	}()
	pre := health.CaptureBaseline(mustParseStatus(t, dataplaneStatusJSON(false)))
	serviceActive = func(string) bool { return true }
	controlSocketReady = func() bool { return true }
	ctlStatusJSON = func() ([]byte, error) {
		return []byte(`{"running":false,"accepting":false,"bridge_ok":false,"tls_ok":false,"quic_ok":false,"tun_ready":false,"cp_connected":false,"healthy":false,"identity_present":true,"version_blocked":false}`), nil
	}
	if _, ok := verifyRollbackHealth(pre, 4); ok {
		t.Fatal("must not report rollback complete while dataplane is worse than baseline")
	}
}

func TestPostUpdateAllowsPreexistingCPDisconnect(t *testing.T) {
	oldActive := serviceActive
	oldSock := controlSocketReady
	oldStatus := ctlStatusJSON
	defer func() {
		serviceActive = oldActive
		controlSocketReady = oldSock
		ctlStatusJSON = oldStatus
	}()
	pre := health.CaptureBaseline(mustParseStatus(t, dataplaneStatusJSON(false)))
	serviceActive = func(string) bool { return true }
	controlSocketReady = func() bool { return true }
	ctlStatusJSON = func() ([]byte, error) { return dataplaneStatusJSON(false), nil }
	res, ok := verifyPostUpdateHealth(pre, 5)
	if !ok || !res.UpdateSuccess || !res.PreexistingManagementDegradation {
		t.Fatalf("res=%+v ok=%v", res, ok)
	}
}

func mustParseStatus(t *testing.T, b []byte) health.Status {
	t.Helper()
	st, err := health.ParseStatusJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
