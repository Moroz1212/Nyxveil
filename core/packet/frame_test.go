package packet_test

import (
	"testing"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/packet"
)

func TestEncodeDecodeInnerPadding(t *testing.T) {
	inner, err := packet.EncodeInner(control.TypeData, []byte("hi"), []byte{9, 9})
	if err != nil {
		t.Fatal(err)
	}
	hdr, payload, pad, err := packet.DecodeInner(inner)
	if err != nil {
		t.Fatal(err)
	}
	if hdr.MsgType != control.TypeData || string(payload) != "hi" || len(pad) != 2 {
		t.Fatalf("roundtrip mismatch: %+v %q %v", hdr, payload, pad)
	}
}

func TestDecodeInnerRejectsFlagMismatch(t *testing.T) {
	data := []byte{control.TypeData, packet.FlagPadding, 0, 0, 1}
	if _, _, _, err := packet.DecodeInner(data); err == nil {
		t.Fatal("padding flag without length must fail")
	}
}

func TestHandshakeEncodeIncludesPadding(t *testing.T) {
	init := packet.HandshakeInitPayload{Version: 1, Padding: []byte{1, 2, 3}}
	b, err := packet.EncodeHandshakeInit(init)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2+32+2+3 {
		t.Fatalf("init size %d", len(b))
	}
	out, err := packet.DecodeHandshakeInit(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Padding) != 3 {
		t.Fatalf("pad=%v", out.Padding)
	}

	resp := packet.HandshakeRespPayload{Version: 1, Epoch: 1, Padding: []byte{4, 5}}
	rb, err := packet.EncodeHandshakeResp(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(rb) == 38 {
		t.Fatal("padded resp must not stay at legacy 38 bytes")
	}
	rout, err := packet.DecodeHandshakeResp(rb)
	if err != nil || rout.Epoch != 1 || len(rout.Padding) != 2 {
		t.Fatalf("resp decode: %+v %v", rout, err)
	}
}

func TestHandshakeDecodeLegacyFixedSize(t *testing.T) {
	legacyInit := make([]byte, 34)
	legacyInit[1] = 1
	if _, err := packet.DecodeHandshakeInit(legacyInit); err != nil {
		t.Fatal(err)
	}
	legacyResp := make([]byte, 38)
	legacyResp[1] = 1
	if _, err := packet.DecodeHandshakeResp(legacyResp); err != nil {
		t.Fatal(err)
	}
}
