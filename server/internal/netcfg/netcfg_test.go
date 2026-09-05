package netcfg

import (
	"net/netip"
	"testing"

	"github.com/nyxveil/server/internal/sessions"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	m := Message{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1420, Gateway: "10.66.0.1"}
	b, err := Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if *got != m {
		t.Fatalf("got %+v want %+v", got, m)
	}
}

func TestValidateRejectsBad(t *testing.T) {
	cases := []Message{
		{VPNIP: "bad", VPNPrefix: 24, MTU: 1400, Gateway: "10.66.0.1"},
		{VPNIP: "10.66.0.2", VPNPrefix: 99, MTU: 1400, Gateway: "10.66.0.1"},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 0, Gateway: "10.66.0.1"},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1400, Gateway: "nope"},
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestFromAllocationAlignsWithSpoofCheck(t *testing.T) {
	cidr := "10.66.0.0/24"
	mgr, err := sessions.New(10, cidr)
	if err != nil {
		t.Fatal(err)
	}
	// Allocate without a real session by using a fake pointer path is hard;
	// instead build message for a known client IP and verify packet parse alignment.
	client := netip.MustParseAddr("10.66.0.2")
	node, err := sessions.NodeAddress(cidr)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := FromAllocation(client, cidr, 1420, node.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if msg.VPNIP != "10.66.0.2" || msg.Gateway != "10.66.0.1" || msg.VPNPrefix != 24 {
		t.Fatalf("%+v", msg)
	}
	// Spoof alignment: build a minimal IPv4 header with src=client and validate.
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	copy(pkt[12:16], client.AsSlice())
	copy(pkt[16:20], netip.MustParseAddr("8.8.8.8").AsSlice())
	src, _, err := sessions.ParseIPv4Endpoints(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if src.String() != msg.VPNIP {
		t.Fatalf("spoof alignment: src=%s vpn_ip=%s", src, msg.VPNIP)
	}
	_ = mgr
}

func TestEncodeRejectsInvalid(t *testing.T) {
	if _, err := Encode(Message{}); err == nil {
		t.Fatal("expected error")
	}
}
