package netcfg

import "testing"

func TestDecodeRequiresDNS(t *testing.T) {
	_, err := Decode([]byte(`{"vpn_ip":"10.66.0.2","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1"}`))
	if err == nil {
		t.Fatal("expected dns_servers required")
	}
}

func TestDecodeOK(t *testing.T) {
	m, err := Decode([]byte(`{"vpn_ip":"10.66.0.2","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1","dns_servers":["203.0.113.53"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.DNSServers) != 1 || m.DNSServers[0] != "203.0.113.53" {
		t.Fatalf("%+v", m)
	}
}
