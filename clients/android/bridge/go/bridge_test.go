package nyxveilbridge

import (
	"testing"
	"time"
)

func TestVersionProtocol(t *testing.T) {
	if Version() == "" {
		t.Fatal("empty version")
	}
	if Protocol() != "NVP/1" {
		t.Fatalf("protocol=%s", Protocol())
	}
	if !NativeReady() {
		t.Fatal("NativeReady expected true")
	}
}

func TestNewEngine(t *testing.T) {
	e := NewEngine()
	if e == nil {
		t.Fatal("nil engine")
	}
	e.Disconnect()
}

func TestWaitCatalogNotBeforeWithinSkew(t *testing.T) {
	start := time.Now()
	issued := start.Add(80 * time.Millisecond)
	waitCatalogNotBefore(issued, 5*time.Minute)
	if time.Now().Before(issued) {
		t.Fatal("should have waited until issued_at")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("wait took too long")
	}
}

func TestWaitCatalogNotBeforeBeyondSkewNoSleep(t *testing.T) {
	start := time.Now()
	issued := start.Add(10 * time.Minute)
	waitCatalogNotBefore(issued, 5*time.Minute)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("must not sleep when skew exceeded")
	}
}
