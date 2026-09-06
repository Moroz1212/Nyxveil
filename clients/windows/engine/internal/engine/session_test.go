package engine_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/netcfg"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/transport"
)

func TestCatalogParseVerifyRoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: "node.example", SPKIPin: make([]byte, 32),
			Endpoints: []transport.Endpoint{{
				Host: "203.0.113.10", Port: 443,
				Profiles: []transport.Profile{transport.ProfileTLSTCP},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}

	keys, err := engine.DecodeCatalogKeys(map[string]string{
		"k1": base64.StdEncoding.EncodeToString(pub),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := catalog.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Verify(keys, parsed); err != nil {
		t.Fatal(err)
	}

	// Wrong key must fail.
	badPub, _, _ := ed25519.GenerateKey(nil)
	badKeys := catalog.VerifyKeys{Keys: map[string]ed25519.PublicKey{"k1": badPub}}
	if err := catalog.Verify(badKeys, parsed); err == nil {
		t.Fatal("expected verify failure with wrong key")
	}

	// Expired catalog must fail.
	expired := signed
	expired.Catalog.IssuedAt = time.Now().UTC().Add(-2 * time.Hour)
	expired.Catalog.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	payload, _ := json.Marshal(expired) // signature won't match canonical anyway after mutate
	_ = payload
	if err := catalog.Verify(keys, expired); err == nil {
		t.Fatal("expected expired/invalid after IssuedAt mutate")
	}
}

func TestTypeConfigDNSRequired(t *testing.T) {
	_, err := netcfg.Decode([]byte(`{"vpn_ip":"10.66.0.2","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1","dns_servers":[]}`))
	if err == nil {
		t.Fatal("empty dns_servers must fail")
	}
	msg, err := netcfg.Decode([]byte(`{"vpn_ip":"10.66.0.2","vpn_prefix":24,"mtu":1420,"gateway":"10.66.0.1","dns_servers":["203.0.113.53"]}`))
	if err != nil {
		t.Fatal(err)
	}
	plan := engine.NewPlan()
	if err := plan.ApplyTypeConfig("Nyxveil", msg.VPNIP, msg.VPNPrefix, msg.Gateway, msg.DNSServers, msg.MTU); err != nil {
		t.Fatal(err)
	}
	if len(plan.TunDNS) != 1 {
		t.Fatalf("dns=%v", plan.TunDNS)
	}
}

func TestRoutePlanOrder(t *testing.T) {
	applier := &engine.NoopApplier{}
	plan := engine.NewPlan()
	if err := applier.Capture(plan); err != nil {
		t.Fatal(err)
	}
	plan.SetBypassHosts([]engine.HostRoute{{}})
	if err := applier.ApplyBypass(plan); err != nil {
		t.Fatal(err)
	}
	if err := plan.ApplyTypeConfig("Nyxveil", "10.66.0.2", 24, "10.66.0.1", []string{"203.0.113.53"}, 1420); err != nil {
		t.Fatal(err)
	}
	if err := applier.ApplyTunnel(plan); err != nil {
		t.Fatal(err)
	}
	if err := applier.Restore(plan); err != nil {
		t.Fatal(err)
	}
	want := []string{"capture", "bypass", "tunnel", "restore"}
	if len(applier.Steps) != len(want) {
		t.Fatalf("steps=%v want %v", applier.Steps, want)
	}
	for i := range want {
		if applier.Steps[i] != want[i] {
			t.Fatalf("steps=%v want %v", applier.Steps, want)
		}
	}
}

func TestEmitNeedAccessTicket(t *testing.T) {
	n := engine.EmitNeedAccessTicket("fi-hel", "reconnect", "Reconnecting")
	if n.Type != "need_access_ticket" || n.DesiredLocationID != "fi-hel" || n.State != "Reconnecting" {
		t.Fatalf("%+v", n)
	}
}

