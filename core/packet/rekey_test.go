package packet_test

import (
	"testing"

	"github.com/nyxveil/nvp/packet"
)

func FuzzDecodeRekeyInit(f *testing.F) {
	f.Add(make([]byte, 36))
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _ = packet.DecodeRekeyInit(data)
	})
}

func FuzzDecodeRekeyAck(f *testing.F) {
	f.Add(make([]byte, 36))
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic: %v", r)
			}
		}()
		_, _ = packet.DecodeRekeyAck(data)
	})
}

func TestRekeyRoundtrip(t *testing.T) {
	p := packet.RekeyInitPayload{Epoch: 2}
	p.EphemeralPub[0] = 0xAB
	b, err := packet.EncodeRekeyInit(p)
	if err != nil {
		t.Fatal(err)
	}
	out, err := packet.DecodeRekeyInit(b)
	if err != nil || out.Epoch != 2 || out.EphemeralPub[0] != 0xAB {
		t.Fatalf("roundtrip failed: %v %v", out, err)
	}
}
