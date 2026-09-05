package netcfg

import (
	"context"
	"unsafe"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/session"
)

// sessionSendControl is linked to the unexported Core method Session.sendControl.
// Requires go.mod: godebug checklinkname=0 (Frozen Core cannot add a push linkname).
//
//go:linkname sessionSendControl github.com/nyxveil/nvp/core/session.(*Session).sendControl
func sessionSendControl(s *session.Session, ctx context.Context, msgType byte, payload []byte) error

// SendConfig pushes TypeConfig JSON to the client after Allocate and before ReadLoop.
func SendConfig(ctx context.Context, sess *session.Session, m Message) error {
	payload, err := Encode(m)
	if err != nil {
		return err
	}
	return sessionSendControl(sess, ctx, control.TypeConfig, payload)
}

// Keep unsafe referenced so the compiler does not strip the linkname trampoline
// on some toolchains that prune unused imports before link.
var _ = unsafe.Sizeof(0)
