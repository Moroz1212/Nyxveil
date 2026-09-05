package revocation

import (
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
)

func TestFailClosedBeforeFirstSync(t *testing.T) {
	c := New()
	if !c.IsRevoked("jti", "lic", "dev") {
		t.Fatal("expected fail-closed before sync")
	}
}

func TestApplyAndLookup(t *testing.T) {
	c := New()
	c.Apply(controlplane.RevocationSnapshot{
		RevokedJTIs:     []string{"j1"},
		RevokedLicenses: []string{"L1"},
		RevokedDevices:  []string{"D1"},
		UpdatedAt:       100,
	})
	if !c.IsRevoked("j1", "", "") {
		t.Fatal("jti")
	}
	if !c.IsRevoked("", "L1", "") {
		t.Fatal("license")
	}
	if !c.IsRevoked("", "", "D1") {
		t.Fatal("device")
	}
	if c.IsRevoked("other", "x", "y") {
		t.Fatal("unexpected revoke")
	}
	if c.SnapshotUpdatedAt() != 100 {
		t.Fatal("updated_at")
	}
}

func TestStaleFailClosed(t *testing.T) {
	c := New()
	c.SetMaxStale(10 * time.Millisecond)
	c.Apply(controlplane.RevocationSnapshot{UpdatedAt: 1})
	if c.IsRevoked("x", "y", "z") {
		t.Fatal("should allow when fresh")
	}
	time.Sleep(20 * time.Millisecond)
	if !c.Stale() {
		t.Fatal("expected stale")
	}
	if !c.IsRevoked("x", "y", "z") {
		t.Fatal("expected fail-closed when stale")
	}
}

func TestApplyReplaces(t *testing.T) {
	c := New()
	c.Apply(controlplane.RevocationSnapshot{RevokedJTIs: []string{"old"}})
	c.Apply(controlplane.RevocationSnapshot{RevokedJTIs: []string{"new"}})
	if c.IsRevoked("old", "", "") {
		t.Fatal("old jti should be gone")
	}
	if !c.IsRevoked("new", "", "") {
		t.Fatal("new jti missing")
	}
}
