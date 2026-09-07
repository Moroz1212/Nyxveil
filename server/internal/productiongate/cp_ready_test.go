package productiongate_test

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/productiongate"
)

func TestGateWaitsForCPReconnect(t *testing.T) {
	var n atomic.Int32
	err := productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 3 * time.Second, Interval: 50 * time.Millisecond, NeedStable: 2,
	}, func() ([]byte, error) {
		i := n.Add(1)
		if i < 4 {
			return []byte(`{"cp_connected":false,"cp_last_error":"temporary"}`), nil
		}
		return []byte(`{"cp_connected":true,"healthy":true}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGateImmediatelyAfterRestart(t *testing.T) {
	var n atomic.Int32
	err := productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 2 * time.Second, Interval: 40 * time.Millisecond, NeedStable: 3,
	}, func() ([]byte, error) {
		if n.Add(1) == 1 {
			return []byte(`{"cp_connected":false}`), nil
		}
		return []byte(`{"cp_connected":true}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGatePassesWhenUpdaterManagementConnected(t *testing.T) {
	status := []byte(`{"cp_connected":true,"cp_url":"https://cp.example:18443","healthy":true}`)
	if err := productiongate.UpdaterAndGateAgree(true, status); err != nil {
		t.Fatal(err)
	}
	// Same truth as updater EvaluatePostUpdate ManagementPlaneConnected.
	st, err := health.ParseStatusJSON(status)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ManagementConnected() {
		t.Fatal("expected management connected")
	}
}

func TestGateAndUpdaterUseSameCPURLField(t *testing.T) {
	raw := []byte(`{"cp_connected":true,"cp_url":"https://cp.nyxveil.ru:18443"}`)
	s, err := productiongate.ParseStatusJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.CPURL != "https://cp.nyxveil.ru:18443" {
		t.Fatalf("cp_url=%q", s.CPURL)
	}
}

func TestGateRejectsWrongHostnamePermanent(t *testing.T) {
	err := productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 500 * time.Millisecond, Interval: 50 * time.Millisecond, NeedStable: 2, FailFast: true,
	}, func() ([]byte, error) {
		return []byte(`{"cp_connected":false,"cp_last_error":"certificate is not valid for host"}`), nil
	})
	if err == nil {
		t.Fatal("expected fail-fast")
	}
}

func TestGateRejectsUntrustedCP(t *testing.T) {
	if !productiongate.PermanentCPError("x509: certificate is not valid for any names") {
		// still matched by "certificate is not valid"
		t.Fatal("expected permanent")
	}
}

func TestGateFailsOnRealAuthFailure(t *testing.T) {
	err := productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 300 * time.Millisecond, Interval: 40 * time.Millisecond, NeedStable: 2,
	}, func() ([]byte, error) {
		return []byte(`{"cp_connected":false,"cp_last_error":"401 unauthorized"}`), nil
	})
	if err == nil || !strings.Contains(err.Error(), "control_plane_reachable") {
		t.Fatalf("got %v", err)
	}
}

func TestGatePassesAfterTransientCPDelay(t *testing.T) {
	start := time.Now()
	var n atomic.Int32
	err := productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 2 * time.Second, Interval: 50 * time.Millisecond, NeedStable: 2,
	}, func() ([]byte, error) {
		if time.Since(start) < 200*time.Millisecond {
			n.Add(1)
			return []byte(`{"cp_connected":false}`), nil
		}
		return []byte(`{"cp_connected":true}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Load() == 0 {
		t.Fatal("expected transient samples")
	}
}

func TestGateNoBusyLoop(t *testing.T) {
	var calls atomic.Int32
	_ = productiongate.WaitCPAuthenticated(productiongate.CPReadyConfig{
		Wait: 250 * time.Millisecond, Interval: 80 * time.Millisecond, NeedStable: 5,
	}, func() ([]byte, error) {
		calls.Add(1)
		return []byte(`{"cp_connected":false}`), errors.New("down")
	})
	c := calls.Load()
	if c < 2 || c > 8 {
		t.Fatalf("unexpected call count %d (busy loop?)", c)
	}
}

func TestInvariantUpdaterConnectedGateMustNotDisagree(t *testing.T) {
	err := productiongate.UpdaterAndGateAgree(true, []byte(`{"cp_connected":false}`))
	if err == nil {
		t.Fatal("expected invariant failure")
	}
}

func TestCurlProbeIsNotAuthoritativeTruth(t *testing.T) {
	// Documented contract: even if auxiliary curl fails, runtime cp_connected wins.
	status := []byte(`{"cp_connected":true,"healthy":true,"cp_url":"https://cp.example"}`)
	if err := productiongate.UpdaterAndGateAgree(true, status); err != nil {
		t.Fatal(err)
	}
	st, _ := productiongate.ParseStatusJSON(status)
	if !st.CPConnected {
		t.Fatal("runtime must be authoritative")
	}
}
