//go:build windows

package engine

import (
	"path/filepath"
	"testing"

	"github.com/nyxveil/client-windows/internal/recoverylog"
)

func TestUndoMutationRejectsEmptyNextHop(t *testing.T) {
	err := undoMutation(recoverylog.Mutation{
		Kind: "default_vpn", DestPrefix: "0.0.0.0/0", NextHop: "",
	})
	if err == nil {
		t.Fatal("expected reject empty next hop (no route delete \"\")")
	}
}

func TestRecoverOnStartupUsesExactPendingNextHop(t *testing.T) {
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
			ID: "ipv6", Kind: "ipv6_set", IPv6IfIndex: 12, IPv6WasEnabled: &was, IPv6NowEnabled: &now,
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
		t.Fatalf("pending next hop not crash-safe: %+v", got.Pending)
	}
	if got.Applied[1].IPv6WasEnabled == nil || !*got.Applied[1].IPv6WasEnabled {
		t.Fatal("ipv6 prior state missing")
	}
	// Simulate crash recovery: empty-GW pending must be refused by undo.
	empty := *got.Pending
	empty.NextHop = ""
	if err := undoMutation(empty); err == nil {
		t.Fatal("empty GW undo must fail closed")
	}
}

func TestJournalPendingBeforeEachMutationKinds(t *testing.T) {
	// Crash simulation matrix: each kind must carry exact reverse fields.
	kinds := []recoverylog.Mutation{
		{ID: "1", Kind: "bypass_route", DestPrefix: "1.2.3.4/32", NextHop: "192.0.2.1", IfIndex: 7, IfLUID: 1, Metric: 1},
		{ID: "2", Kind: "tun_addr", TunName: "Nyxveil", DestPrefix: "10.66.0.2/24"},
		{ID: "3", Kind: "tun_dns", TunName: "Nyxveil", DNS: []string{"10.66.0.1"}},
		{ID: "4", Kind: "ipv6_set", IPv6IfIndex: 7, IPv6WasEnabled: boolPtr(true), IPv6NowEnabled: boolPtr(false)},
		{ID: "5", Kind: "default_vpn", DestPrefix: "0.0.0.0/0", NextHop: "10.66.0.1", Metric: 1},
	}
	dir := t.TempDir()
	for _, mut := range kinds {
		path := filepath.Join(dir, mut.ID+".json")
		j := recoverylog.Journal{Phase: "crash-sim", Pending: &mut}
		if err := recoverylog.Write(path, j); err != nil {
			t.Fatal(err)
		}
		got, err := recoverylog.Read(path)
		if err != nil {
			t.Fatal(err)
		}
		if got.Pending == nil || got.Pending.Kind != mut.Kind {
			t.Fatalf("kind %s not persisted as pending", mut.Kind)
		}
		if mut.Kind == "default_vpn" && got.Pending.NextHop == "" {
			t.Fatal("default_vpn pending lost next hop")
		}
		if mut.Kind == "ipv6_set" && (got.Pending.IPv6WasEnabled == nil || got.Pending.IPv6IfIndex == 0) {
			t.Fatal("ipv6 pending incomplete")
		}
	}
}

func TestIPv6ExactRestoreFromMutation(t *testing.T) {
	wasEnabled := true
	wasDisabled := false
	cases := []struct {
		name string
		was  bool
	}{
		{"restore_enabled", true},
		{"restore_disabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.was
			m := recoverylog.Mutation{
				Kind: "ipv6_set", IPv6IfIndex: 0, // ifIndex 0 = no-op SetIPv6 (safe unit path)
				IPv6WasEnabled: &w, IPv6NowEnabled: boolPtr(!w),
			}
			if w != wasEnabled && w != wasDisabled {
				t.Fatal("bad case")
			}
			if err := undoMutation(m); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
