package failover_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/failover"
	"github.com/nyxveil/nvp/core/nvperr"
	"github.com/nyxveil/nvp/core/transport"
)

func TestExhaustedErrorWrapsTransportUnavailableAndListsTriedNodes(t *testing.T) {
	sel := &failover.Selector{
		LocationID: "fi-hel",
		Catalog: model.Catalog{
			Nodes: []model.NodeRegistryEntry{
				{NodeID: "n-a", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 1}}},
				{NodeID: "n-b", LocationID: "fi-hel", Enabled: true, Capacity: 10,
					Endpoints: []transport.Endpoint{{Host: "127.0.0.1", Port: 2}}},
			},
		},
	}
	policy := failover.ConnectPolicy{
		MaxNodeAttempts: 2,
		RetryDelay:      time.Millisecond,
		TransportRacing: transport.RacingConfig{Primary: transport.ProfileTLSTCP, Fallback: transport.ProfileTLSTCP},
	}
	reg := transport.NewRegistry()
	reg.Register(&failAllTransport{})
	_, _, err := failover.ConnectWithFailover(context.Background(), sel, reg, policy, nil)
	if err == nil {
		t.Fatal("expected exhausted error")
	}
	var ex *failover.ExhaustedError
	if !errors.As(err, &ex) {
		t.Fatalf("want ExhaustedError, got %T %v", err, err)
	}
	if !errors.Is(err, nvperr.ErrTransportUnavailable) {
		t.Fatalf("want ErrTransportUnavailable unwrap, got %v", err)
	}
	if len(ex.TriedNodes) < 2 {
		t.Fatalf("expected both nodes tried, got %v", ex.TriedNodes)
	}
	joined := strings.Join(ex.TriedNodes, ",")
	if !strings.Contains(joined, "n-a") || !strings.Contains(joined, "n-b") {
		t.Fatalf("tried nodes missing: %v", ex.TriedNodes)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("error must not include secrets: %v", err)
	}
}

func TestExhaustedErrorNoHealthyNodes(t *testing.T) {
	sel := &failover.Selector{
		LocationID: "fi-hel",
		Catalog:    model.Catalog{Nodes: nil},
	}
	policy := failover.DefaultConnectPolicy()
	reg := transport.NewRegistry()
	_, _, err := failover.ConnectWithFailover(context.Background(), sel, reg, policy, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, nvperr.ErrNoHealthyNodes) {
		t.Fatalf("want ErrNoHealthyNodes, got %v", err)
	}
}

type failAllTransport struct{}

func (t *failAllTransport) Profile() transport.Profile { return transport.ProfileTLSTCP }
func (t *failAllTransport) Dial(context.Context, transport.DialConfig) (transport.Conn, error) {
	return nil, fmt.Errorf("dial failed")
}
func (t *failAllTransport) Listen(context.Context, string, interface{}) (transport.Listener, error) {
	return nil, fmt.Errorf("not implemented")
}
