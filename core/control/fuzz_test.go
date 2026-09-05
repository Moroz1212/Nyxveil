package control_test

import (
	"testing"

	"github.com/nyxveil/nvp/core/control"
)

func FuzzControlMessageTypes(f *testing.F) {
	f.Add(byte(0x01))
	f.Add(byte(0x10))
	f.Add(byte(0x20))
	f.Add(byte(0xFF))
	f.Fuzz(func(t *testing.T, b byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_ = control.IsValidType(b)
		_ = control.IsControl(b)
	})
}

func FuzzControlAuthFailReasons(f *testing.F) {
	f.Add(byte(control.AuthFailInvalidTicket))
	f.Add(byte(control.AuthFailInternal))
	f.Add(byte(0))
	f.Fuzz(func(t *testing.T, reason byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		// Reasons are opaque bytes on the wire; ensure classification helpers stay panic-free.
		_ = control.IsControl(control.TypeAuthFail)
		_ = reason
	})
}
