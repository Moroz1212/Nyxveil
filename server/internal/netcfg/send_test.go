package netcfg

import (
	"context"
	"testing"

	"github.com/nyxveil/nvp/core/session"
)

// TestSendConfigLinknameResolves ensures the go:linkname trampoline to
// session.sendControl is linked (call fails with ErrAuthRequired without keys,
// not with a missing-symbol linker error).
func TestSendConfigLinknameResolves(t *testing.T) {
	sess := session.New(session.DefaultConfig(false))
	err := SendConfig(context.Background(), sess, Message{
		VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1420, Gateway: "10.66.0.1",
	})
	if err == nil {
		t.Fatal("expected auth/session error without established session")
	}
}
