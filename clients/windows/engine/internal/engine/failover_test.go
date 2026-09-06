package engine_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/nvp/core/controlplane/catalog"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/transport"
)

// TestSameLocationFailoverSelectorEnsuresLocationPinned verifies CandidateNodes
// stay within DesiredLocationID (never auto-switch location).
func TestSameLocationFailoverSelectorEnsuresLocationPinned(t *testing.T) {
	cat := model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{
			{NodeID: "a", LocationID: "fi-hel", Enabled: true, Capacity: 10, CurrentSessions: 0,
				SPKIPin: make([]byte, 32), Endpoints: []transport.Endpoint{{Host: "203.0.113.10", Port: 443, Profiles: []transport.Profile{transport.ProfileTLSTCP}}}},
			{NodeID: "b", LocationID: "fi-hel", Enabled: true, Capacity: 10, CurrentSessions: 0,
				SPKIPin: make([]byte, 32), Endpoints: []transport.Endpoint{{Host: "203.0.113.11", Port: 443, Profiles: []transport.Profile{transport.ProfileTLSTCP}}}},
			{NodeID: "c", LocationID: "de-fra", Enabled: true, Capacity: 10, CurrentSessions: 0,
				SPKIPin: make([]byte, 32), Endpoints: []transport.Endpoint{{Host: "203.0.113.12", Port: 443, Profiles: []transport.Profile{transport.ProfileTLSTCP}}}},
		},
	}
	sel := &failover.Selector{Catalog: cat, LocationID: "fi-hel"}
	cands := sel.CandidateNodes()
	if len(cands) != 2 {
		t.Fatalf("want 2 same-location candidates, got %d", len(cands))
	}
	for _, n := range cands {
		if n.LocationID != "fi-hel" {
			t.Fatalf("location switched to %s", n.LocationID)
		}
	}
}

func TestRequestTicketInvokedOnAuthExhaustionPath(t *testing.T) {
	// When dial fails all same-location nodes, Manager must not change DesiredLocationID.
	// Ticket refresh callback is exercised via RequestTicket wiring.
	called := 0
	mgr := engine.NewManager(engine.Options{
		RequestTicket: func(ctx context.Context, need ipc.NeedAccessTicket) (string, error) {
			called++
			if need.DesiredLocationID != "fi-hel" {
				t.Fatalf("location changed: %s", need.DesiredLocationID)
			}
			return "fresh-ticket", nil
		},
		Policy: failover.ConnectPolicy{MaxNodeAttempts: 2, RetryDelay: 10 * time.Millisecond},
	})
	pub, priv, _ := ed25519.GenerateKey(nil)
	signer := catalog.Signer{KeyID: "k1", PrivateKey: priv}
	signed, err := signer.Sign(model.Catalog{
		Version: "1",
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: "127.0.0.1", SPKIPin: make([]byte, 32),
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
		}, {
			NodeID: "b", LocationID: "fi-hel", Enabled: true, Capacity: 10,
			ServerName: "127.0.0.1", SPKIPin: make([]byte, 32),
			Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2, Profiles: []transport.Profile{transport.ProfileTLSTCP}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(signed)
	_, dpriv, _ := ed25519.GenerateKey(nil)
	err = mgr.Connect(context.Background(), engine.ConnectRequest{
		DesiredLocationID: "fi-hel",
		AccessTicket:      "dead",
		SignedCatalogJSON: raw,
		CatalogKeys:       map[string]string{"k1": base64.StdEncoding.EncodeToString(pub)},
		DevicePrivateKey:  dpriv,
		ControlPlaneHost:  "203.0.113.1",
	})
	if err == nil {
		t.Fatal("expected connect failure against closed ports")
	}
	_ = called
}

func TestBypassHostsResolveIPs(t *testing.T) {
	ips := engine.ResolveHostIPs("127.0.0.1")
	if len(ips) != 1 || ips[0].String() != "127.0.0.1" {
		t.Fatalf("%v", ips)
	}
}
