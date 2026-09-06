package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/nvp/core/tunnel"
)

func TestTunOpenNotLinkedOrSkip(t *testing.T) {
	f := engine.NewTunFactory()
	dev, err := f.Open(context.Background(), tunnel.Config{Name: "Nyxveil", MTU: 1420})
	if err == nil {
		_ = dev.Close()
		t.Skip("Wintun driver appears linked; skipping not-linked assertion")
	}
	if errors.Is(err, engine.ErrNotLinked) {
		return
	}
	// OpenFunc is linked; missing wintun.dll is the expected lab failure mode.
	if strings.Contains(strings.ToLower(err.Error()), "wintun") {
		return
	}
	t.Fatalf("unexpected TUN open error: %v", err)
}
