package nyxveilbridge

import (
	"net/netip"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestShouldForwardIPv4Only(t *testing.T) {
	vpn := netip.MustParseAddr("10.66.0.34")
	ipv6 := []byte{0x60, 0, 0, 0}
	if ok, reason := shouldForwardTunnelPacket(ipv6, vpn); ok || reason != "non_ipv4" {
		t.Fatalf("ipv6: ok=%v reason=%s", ok, reason)
	}
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	copy(pkt[12:16], []byte{10, 66, 0, 34})
	copy(pkt[16:20], []byte{1, 1, 1, 1})
	if ok, _ := shouldForwardTunnelPacket(pkt, vpn); !ok {
		t.Fatal("expected forward")
	}
	pkt[15] = 99
	if ok, reason := shouldForwardTunnelPacket(pkt, vpn); ok || reason != "spoofed_source" {
		t.Fatalf("spoof: ok=%v reason=%s", ok, reason)
	}
}

func TestParseTunnelPacketMetaIPv4(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 17
	copy(pkt[12:16], []byte{10, 66, 0, 34})
	copy(pkt[16:20], []byte{1, 1, 1, 1})
	m := parseTunnelPacketMeta(pkt)
	if m.Version != 4 || m.Protocol != 17 || m.Length != 20 {
		t.Fatalf("meta=%+v", m)
	}
}

func TestIsAgainRecognizesEAGAIN(t *testing.T) {
	if !isAgain(syscall.EAGAIN) {
		t.Fatal("EAGAIN should be recoverable")
	}
}

func TestTunFilePipeReadWrite(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	// Steal read FD into tunFile (simulates VpnService detachFd ownership).
	rFd := int(r.Fd())
	// Dup so closing tunFile doesn't break test cleanup oddly on all platforms.
	dev, err := newTunFileFromOSFile(r, 1420)
	if err != nil {
		t.Fatal(err)
	}
	_ = rFd

	go func() {
		time.Sleep(20 * time.Millisecond)
		pkt := make([]byte, 20)
		pkt[0] = 0x45
		_, _ = w.Write(pkt)
	}()

	buf := make([]byte, 2048)
	n, err := dev.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 20 || buf[0]>>4 != 4 {
		t.Fatalf("n=%d ver=%d", n, buf[0]>>4)
	}

	// RX path: write into pipe from "native" side via second pipe.
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	dev2, err := newTunFileFromOSFile(w2, 1420)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 20)
	out[0] = 0x45
	if _, err := dev2.Write(out); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 20)
	n, err = r2.Read(got)
	if err != nil || n != 20 || got[0] != 0x45 {
		t.Fatalf("rx n=%d err=%v", n, err)
	}
}

func TestTunCloseUnblocksRead(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = w
	dev, err := newTunFileFromOSFile(r, 1280)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		_, err := dev.Read(buf)
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	_ = dev.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read not unblocked by Close")
	}
}

func TestDataplaneCountersSnapshot(t *testing.T) {
	var c dataplaneCounters
	c.tunReadPackets.Store(3)
	c.nvpTxPackets.Store(2)
	m := c.snapshotMap()
	if m["tun_read_packets"].(uint64) != 3 {
		t.Fatal(m)
	}
	if m["traffic_idle"].(bool) {
		t.Fatal("should not be idle")
	}
}

// newTunFileFromOSFile wraps an existing *os.File without re-owning via raw fd SetNonblock.
func newTunFileFromOSFile(f *os.File, mtu int) (*tunFile, error) {
	if f == nil {
		return nil, os.ErrInvalid
	}
	return &tunFile{f: f, mtu: mtu, fd: int(f.Fd())}, nil
}
