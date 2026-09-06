package netcfg

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/nyxveil/server/internal/sessions"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	m := Message{
		VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1420, Gateway: "10.66.0.1",
		DNSServers: []string{"203.0.113.53"},
	}
	b, err := Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, m) {
		t.Fatalf("got %+v want %+v", got, m)
	}
}

func TestValidateRejectsBad(t *testing.T) {
	cases := []Message{
		{VPNIP: "bad", VPNPrefix: 24, MTU: 1400, Gateway: "10.66.0.1", DNSServers: []string{"203.0.113.53"}},
		{VPNIP: "10.66.0.2", VPNPrefix: 99, MTU: 1400, Gateway: "10.66.0.1", DNSServers: []string{"203.0.113.53"}},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 0, Gateway: "10.66.0.1", DNSServers: []string{"203.0.113.53"}},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1400, Gateway: "nope", DNSServers: []string{"203.0.113.53"}},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1400, Gateway: "10.66.0.1"}, // empty dns
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1400, Gateway: "10.66.0.1", DNSServers: []string{"not-an-ip"}},
		{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1400, Gateway: "10.66.0.1", DNSServers: []string{"2001:db8::1"}}, // IPv6 not accepted in 1.0.2
	}
	for _, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("expected error for %+v", c)
		}
	}
}

func TestValidateRejectsEmptyDNSServers(t *testing.T) {
	m := Message{VPNIP: "10.66.0.2", VPNPrefix: 24, MTU: 1420, Gateway: "10.66.0.1", DNSServers: nil}
	if err := m.Validate(); err == nil {
		t.Fatal("expected dns_servers required")
	}
}

func TestFromAllocationRequiresDNS(t *testing.T) {
	client := netip.MustParseAddr("10.66.0.2")
	gw := netip.MustParseAddr("10.66.0.1")
	if _, err := FromAllocation(client, "10.66.0.0/24", 1420, gw, nil); err == nil {
		t.Fatal("expected error without dns_servers")
	}
	if _, err := FromAllocation(client, "10.66.0.0/24", 1420, gw, []string{}); err == nil {
		t.Fatal("expected error with empty dns_servers")
	}
}

func TestFromAllocationAlignsWithSpoofCheck(t *testing.T) {
	cidr := "10.66.0.0/24"
	mgr, err := sessions.New(10, cidr)
	if err != nil {
		t.Fatal(err)
	}
	client := netip.MustParseAddr("10.66.0.2")
	node, err := sessions.NodeAddress(cidr)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := FromAllocation(client, cidr, 1420, node.Addr(), []string{"203.0.113.53"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.VPNIP != "10.66.0.2" || msg.Gateway != "10.66.0.1" || msg.VPNPrefix != 24 {
		t.Fatalf("%+v", msg)
	}
	if len(msg.DNSServers) != 1 || msg.DNSServers[0] != "203.0.113.53" {
		t.Fatalf("dns=%v", msg.DNSServers)
	}
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	copy(pkt[12:16], client.AsSlice())
	copy(pkt[16:20], netip.MustParseAddr("198.51.100.1").AsSlice())
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
