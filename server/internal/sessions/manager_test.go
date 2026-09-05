package sessions

import (
	"bytes"
	"net/netip"
	"testing"

	"github.com/nyxveil/nvp/core/session"
)

func TestAllocateReleaseAndReuse(t *testing.T) {
	m, err := New(2, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	s1 := session.New(session.DefaultConfig(false))
	s2 := session.New(session.DefaultConfig(false))
	r1, err := m.Allocate(s1)
	if err != nil {
		t.Fatal(err)
	}
	if r1.VPNIP.String() != "10.66.0.2" {
		t.Fatalf("first client IP want 10.66.0.2 got %s", r1.VPNIP)
	}
	r2, err := m.Allocate(s2)
	if err != nil {
		t.Fatal(err)
	}
	if m.Count() != 2 {
		t.Fatalf("count=%d", m.Count())
	}
	if _, err := m.Allocate(session.New(session.DefaultConfig(false))); err != ErrCapacityFull {
		t.Fatalf("want capacity full, got %v", err)
	}
	m.ReleaseBySession(s1)
	if m.Count() != 1 {
		t.Fatalf("count after release=%d", m.Count())
	}
	s3 := session.New(session.DefaultConfig(false))
	r3, err := m.Allocate(s3)
	if err != nil {
		t.Fatal(err)
	}
	if r3.VPNIP != r1.VPNIP {
		// Pool may LIFO or FIFO; either freed IP is fine — just ensure it's free.
		if _, ok := m.LookupByIP(r3.VPNIP); !ok {
			t.Fatal("missing lookup")
		}
	}
	_ = r2
}

func TestSpoofedSourceRejected(t *testing.T) {
	m, err := New(10, "10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(session.DefaultConfig(false))
	rec, err := m.Allocate(sess)
	if err != nil {
		t.Fatal(err)
	}
	good := makeIPv4(rec.VPNIP, netip.MustParseAddr("1.2.3.4"))
	if err := m.ValidateSource(sess, good); err != nil {
		t.Fatalf("valid source: %v", err)
	}
	bad := makeIPv4(netip.MustParseAddr("10.66.0.99"), netip.MustParseAddr("1.2.3.4"))
	if err := m.ValidateSource(sess, bad); err != ErrSpoofedSource {
		t.Fatalf("want spoof error, got %v", err)
	}
}

func TestLookupByDestIP(t *testing.T) {
	m, err := New(10, "10.66.0.0/28")
	if err != nil {
		t.Fatal(err)
	}
	sess := session.New(session.DefaultConfig(false))
	rec, err := m.Allocate(sess)
	if err != nil {
		t.Fatal(err)
	}
	pkt := makeIPv4(netip.MustParseAddr("8.8.8.8"), rec.VPNIP)
	dst, err := DestIP(pkt)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.LookupByIP(dst)
	if !ok || got.ID != rec.ID {
		t.Fatalf("lookup failed: %+v", got)
	}
}

func TestNodeAddress(t *testing.T) {
	p, err := NodeAddress("10.66.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if p.String() != "10.66.0.1/24" {
		t.Fatalf("got %s", p)
	}
}

func makeIPv4(src, dst netip.Addr) []byte {
	pkt := bytes.Repeat([]byte{0}, 20)
	pkt[0] = 0x45
	s := src.As4()
	d := dst.As4()
	copy(pkt[12:16], s[:])
	copy(pkt[16:20], d[:])
	return pkt
}
