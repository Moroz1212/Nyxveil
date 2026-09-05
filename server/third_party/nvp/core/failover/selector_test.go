package failover

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/transport"
)

func TestCandidateNodesAllowsUnknownHealth(t *testing.T) {
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "a", LocationID: "fi-hel", Enabled: true, Capacity: 10},
				{NodeID: "b", LocationID: "fi-hel", Enabled: true, Capacity: 10, Health: model.HealthInfo{Healthy: false}, LastSeen: time.Now()},
			},
		},
	}
	got := sel.CandidateNodes()
	if len(got) != 1 || got[0].NodeID != "a" {
		t.Fatalf("want only unknown-health node a, got %+v", got)
	}
}

func TestSelectNodeNoDial(t *testing.T) {
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "busy", LocationID: "fi-hel", Enabled: true, Capacity: 10, CurrentSessions: 9, SPKIPin: []byte{9}},
				{NodeID: "free", LocationID: "fi-hel", Enabled: true, Capacity: 10, CurrentSessions: 1, SPKIPin: []byte{1}},
			},
		},
	}
	node, err := sel.SelectNode()
	if err != nil {
		t.Fatal(err)
	}
	if node.NodeID != "free" {
		t.Fatalf("want lowest-load node free, got %s", node.NodeID)
	}
}

func TestSameLocationNodeFailoverKeepsTicket(t *testing.T) {
	// Same-location candidates share one location-scoped ticket (NodeScope empty).
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "a", LocationID: "fi-hel", Enabled: true, Capacity: 10},
				{NodeID: "b", LocationID: "fi-hel", Enabled: true, Capacity: 10},
			},
		},
	}
	nodes := sel.CandidateNodes()
	if len(nodes) != 2 {
		t.Fatalf("want 2 same-location nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.LocationID != "fi-hel" {
			t.Fatalf("ticket location scope broken: %s", n.LocationID)
		}
	}
}

// TestConnectorDoesNotAutomaticallyCrossLocations: FI NodeA down, DE NodeB up,
// DesiredLocation=FI → ErrNoHealthyNodes (never selects NodeB in DE).
func TestConnectorDoesNotAutomaticallyCrossLocations(t *testing.T) {
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "fi-a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Health: model.HealthInfo{Healthy: false}, LastSeen: time.Now(),
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}}},
				{NodeID: "de-b", LocationID: "de-fra", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2}}},
			},
		},
	}
	ft := &portAwareTransport{succeedPort: 2}
	reg := transport.NewRegistry()
	reg.Register(ft)
	policy := ConnectPolicy{
		MaxNodeAttempts: 3,
		RetryDelay:      time.Millisecond,
		TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
	}
	_, node, err := ConnectWithFailover(context.Background(), sel, reg, policy, nil)
	if err == nil {
		t.Fatalf("expected ErrNoHealthyNodes, got node %s", node.NodeID)
	}
	if !errors.Is(err, nvperr.ErrNoHealthyNodes) {
		t.Fatalf("want ErrNoHealthyNodes, got %v", err)
	}
	if ft.port2Attempts != 0 {
		t.Fatal("must not dial de-fra node when DesiredLocation=fi-hel")
	}
	if node.NodeID == "de-b" {
		t.Fatal("must not select cross-location node de-b")
	}
}

// TestNodeScopedTicketPreventsFailoverToOtherNode: NodeScope=[A], A down B up → fail.
func TestNodeScopedTicketPreventsFailoverToOtherNode(t *testing.T) {
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}}},
				{NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2}}},
			},
		},
	}
	ft := &portAwareTransport{succeedPort: 2}
	reg := transport.NewRegistry()
	reg.Register(ft)
	policy := ConnectPolicy{
		MaxNodeAttempts: 3,
		AllowedNodeIDs:  []string{"node-a"},
		RetryDelay:      time.Millisecond,
		TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
	}
	_, node, err := ConnectWithFailover(context.Background(), sel, reg, policy, nil)
	if err == nil {
		t.Fatalf("expected failure when only scoped node is down, got %s", node.NodeID)
	}
	if ft.port2Attempts != 0 {
		t.Fatal("must not dial node-b when NodeScope=[node-a]")
	}
}

// TestLocationScopedTicketAllowsSameLocationNodeFailover: empty NodeScope, A down B up → B.
func TestLocationScopedTicketAllowsSameLocationNodeFailover(t *testing.T) {
	sel := &Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "node-a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}}},
				{NodeID: "node-b", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2}}},
			},
		},
	}
	ft := &portAwareTransport{succeedPort: 2}
	reg := transport.NewRegistry()
	reg.Register(ft)
	policy := ConnectPolicy{
		MaxNodeAttempts: 3,
		RetryDelay:      time.Millisecond,
		TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
	}
	_, node, err := ConnectWithFailover(context.Background(), sel, reg, policy, nil)
	if err != nil {
		t.Fatalf("same-location failover: %v", err)
	}
	if node.NodeID != "node-b" {
		t.Fatalf("expected node-b, got %s", node.NodeID)
	}
}

type portAwareTransport struct {
	succeedPort   int
	port2Attempts int
}

func (t *portAwareTransport) Profile() transport.Profile { return transport.ProfileTLSTCP }
func (t *portAwareTransport) Dial(_ context.Context, cfg transport.DialConfig) (transport.Conn, error) {
	if cfg.Endpoint.Port == 2 {
		t.port2Attempts++
	}
	if t.succeedPort != 0 && cfg.Endpoint.Port == t.succeedPort {
		return &fakeConn{}, nil
	}
	return nil, fmt.Errorf("dial failed port=%d", cfg.Endpoint.Port)
}
func (t *portAwareTransport) Listen(context.Context, string, interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("not implemented")
}

type fakeConn struct{}

func (c *fakeConn) Read(context.Context) ([]byte, error) { return nil, fmt.Errorf("eof") }
func (c *fakeConn) Write(context.Context, []byte) error  { return nil }
func (c *fakeConn) Close() error                         { return nil }
func (c *fakeConn) LocalAddr() net.Addr                  { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (c *fakeConn) RemoteAddr() net.Addr                 { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (c *fakeConn) Profile() transport.Profile           { return transport.ProfileTLSTCP }
func (c *fakeConn) SetReadDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error     { return nil }
