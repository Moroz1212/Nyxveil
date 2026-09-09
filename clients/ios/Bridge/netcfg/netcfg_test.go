package netcfg

import "testing"

func TestIsNetworkBase(t *testing.T) {
	if !IsNetworkBase("10.66.0.0", 24) {
		t.Fatal("expected network base")
	}
	if IsNetworkBase("10.66.0.25", 24) {
		t.Fatal("host IP must not be treated as network base")
	}
}

func TestDecodeRequiresDNS(t *testing.T) {
	_, err := Decode([]byte(`{"vpn_ip":"10.66.0.25","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1","dns_servers":[]}`))
	if err == nil {
		t.Fatal("expected dns required")
	}
}
