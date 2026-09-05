package packet_test

import (
	"testing"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/packet"
)

func FuzzDecodeWireRecord(f *testing.F) {
	f.Add([]byte{0, 0, 0, 16, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4})
	f.Add([]byte{})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic on input len=%d: %v", len(data), r)
			}
		}()
		_, _, _ = packet.DecodeWireRecord(data)
	})
}

func FuzzDecodeInner(f *testing.F) {
	f.Add([]byte{control.TypeData, 0, 0, 0, 1, 2, 3})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _, _, _ = packet.DecodeInner(data)
	})
}

func FuzzDecodeHandshakeInit(f *testing.F) {
	f.Add([]byte{0, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32})
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _ = packet.DecodeHandshakeInit(data)
	})
}

func FuzzDecodeHandshakeResp(f *testing.F) {
	f.Add(make([]byte, 38))
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _ = packet.DecodeHandshakeResp(data)
	})
}
