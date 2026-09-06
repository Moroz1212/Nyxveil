package recoverylog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nyxveil/client-windows/internal/recoverylog"
)

func TestJournalCrashPendingAndApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.json")
	was := true
	now := false
	j := recoverylog.Journal{
		Phase: "tunnel",
		Pending: &recoverylog.Mutation{
			ID: "m-pending", Kind: "default_vpn",
			DestPrefix: "0.0.0.0/0", NextHop: "10.66.0.1", Metric: 1,
		},
		Applied: []recoverylog.Mutation{{
			ID: "m1", Kind: "bypass_route",
			DestPrefix: "203.0.113.10/32", NextHop: "192.0.2.1", IfIndex: 12, IfLUID: 99, Metric: 1,
		}, {
			ID: "m2", Kind: "ipv6_set", IPv6IfIndex: 12, IPv6WasEnabled: &was, IPv6NowEnabled: &now,
		}},
		OriginalDefault: &recoverylog.Mutation{
			Kind: "original_default", DestPrefix: "0.0.0.0/0", NextHop: "192.0.2.1", IfIndex: 12, Metric: 25,
		},
	}
	if err := recoverylog.Write(path, j); err != nil {
		t.Fatal(err)
	}
	got, err := recoverylog.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pending == nil || got.Pending.NextHop != "10.66.0.1" {
		t.Fatalf("pending=%+v", got.Pending)
	}
	if len(got.Applied) != 2 || got.Applied[0].IfIndex != 12 {
		t.Fatalf("applied=%+v", got.Applied)
	}
	if got.OriginalDefault == nil || got.OriginalDefault.NextHop == "" {
		t.Fatal("original default missing next hop")
	}
	if err := recoverylog.Clear(path); err != nil {
		t.Fatal(err)
	}
	// Avoid Windows AV holding the dir open during TempDir cleanup.
	_ = os.RemoveAll(dir)
}
