package engine

import (
	"encoding/binary"
	"testing"
)

func TestBuildICMPv4FragNeeded(t *testing.T) {
	orig := make([]byte, 40)
	orig[0] = 0x45
	binary.BigEndian.PutUint16(orig[2:4], 40)
	orig[9] = 6
	copy(orig[12:16], []byte{10, 66, 0, 24})
	copy(orig[16:20], []byte{1, 1, 1, 1})
	copy(orig[20:28], []byte{0, 80, 1, 2, 3, 4, 5, 6})

	out := buildICMPv4FragNeeded(orig, 1200)
	if out == nil {
		t.Fatal("nil icmp")
	}
	if out[9] != 1 || out[20] != 3 || out[21] != 4 {
		t.Fatalf("bad icmp header")
	}
	if binary.BigEndian.Uint16(out[26:28]) != 1200 {
		t.Fatalf("mtu field=%d", binary.BigEndian.Uint16(out[26:28]))
	}
	// src=orig dst, dst=orig src
	if out[12] != 1 || out[16] != 10 {
		t.Fatalf("addrs swapped incorrectly: %v %v", out[12:16], out[16:20])
	}
}
