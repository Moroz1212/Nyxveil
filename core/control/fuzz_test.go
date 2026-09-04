package control_test

import (
	"testing"

	"github.com/nyxveil/nvp/control"
)

func FuzzControlMessageTypes(f *testing.F) {
	f.Add(byte(0x01))
	f.Add(byte(0xFF))
	f.Fuzz(func(t *testing.T, b byte) {
		_ = control.IsValidType(b)
		_ = control.IsControl(b)
	})
}
