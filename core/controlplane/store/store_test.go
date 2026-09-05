package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/controlplane/store"
	_ "modernc.org/sqlite"
)

func TestStoreLicenseAndDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	lic := model.LicenseRecord{
		LicenseID:  "nyx_lic_1",
		Plan:       "premium",
		MaxDevices: 2,
		Enabled:    true,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}
	if err := st.UpsertLicense(ctx, lic); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetLicense(ctx, "nyx_lic_1")
	if err != nil || got.Plan != "premium" {
		t.Fatalf("license: %v %v", got, err)
	}

	dev := model.DeviceRecord{
		DeviceID:   "dev_1",
		LicenseID:  "nyx_lic_1",
		PublicKey:  []byte{1},
		Enabled:    true,
		Registered: time.Now().UTC(),
	}
	if err := st.RegisterDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}
	n, _ := st.CountDevices(ctx, "nyx_lic_1")
	if n != 1 {
		t.Fatalf("expected 1 device, got %d", n)
	}
}

func TestStoreLicenseSecretEncryption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	kek := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	st, err := store.OpenWithKEK(path, kek)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lic := model.LicenseRecord{
		LicenseID:  "nyx_lic_enc",
		Plan:       "premium",
		MaxDevices: 1,
		Enabled:    true,
		ExpiresAt:  time.Now().Add(24 * time.Hour),
		Secret:     "super-secret",
	}
	if err := st.UpsertLicense(ctx, lic); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetLicense(ctx, "nyx_lic_enc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Secret, "hmac1:") {
		t.Fatalf("expected HMAC verifier storage, got %q", got.Secret)
	}
	ok, err := st.MatchSecret(got.Secret, "super-secret")
	if err != nil || !ok {
		t.Fatalf("match good secret: %v %v", ok, err)
	}
	ok, err = st.MatchSecret(got.Secret, "wrong")
	if err != nil || ok {
		t.Fatal("wrong secret must not match")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	plain, err := store.OpenWithKEK(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	got2, err := plain.GetLicense(ctx, "nyx_lic_enc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.MatchSecret(got2.Secret, "super-secret"); err == nil {
		t.Fatal("HMAC match without KEK must fail")
	}
}

func TestOpenProductionRequiresKEK(t *testing.T) {
	t.Setenv("NVP_LICENSE_KEK", "")
	path := filepath.Join(t.TempDir(), "prod.db")
	st, err := store.OpenProduction(path)
	if err == nil {
		_ = st.Close()
		t.Fatal("OpenProduction must fail without KEK")
	}
	t.Setenv("NVP_LICENSE_KEK", "not-a-valid-key")
	st, err = store.OpenProduction(path)
	if err == nil {
		_ = st.Close()
		t.Fatal("OpenProduction must fail with invalid KEK")
	}
	kek := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	t.Setenv("NVP_LICENSE_KEK", kek)
	st, err = store.OpenProduction(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.UpsertLicense(ctx, model.LicenseRecord{
		LicenseID: "lic_prod", Plan: "basic", MaxDevices: 1, Enabled: true,
		ExpiresAt: time.Now().Add(time.Hour), Secret: "s3cret",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	node := model.NodeRegistryEntry{
		NodeID:     "fi-hel-01",
		LocationID: "fi-hel",
		Enabled:    true,
		Capacity:   100,
		LastSeen:   time.Now().UTC(),
	}
	if err := st.UpsertNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.ListNodes(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
}

func TestNodePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pin := []byte{0x01, 0x02, 0x03, 0x04, 0xaa, 0xbb, 0xcc, 0xdd}
	want := model.NodeRegistryEntry{
		NodeID:          "fi-hel-01",
		LocationID:      "fi-hel",
		Country:         "FI",
		City:            "Helsinki",
		DisplayName:     "Helsinki 01",
		Status:          "healthy",
		Enabled:         true,
		TestOnly:        false,
		Draining:        true,
		ProtocolVersion: 1,
		ServerVersion:   "1.2.3",
		ServerName:      "vpn.helsinki.example",
		SPKIPin:         pin,
		Capacity:        200,
		CurrentSessions: 5,
		Health:          model.HealthInfo{Healthy: true, SessionCount: 5, CPUPercent: 12},
		LastSeen:        time.Now().UTC().Truncate(time.Second),
	}
	if err := st.CreateOrUpdateNodeConfig(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	got, err := st2.GetNode(ctx, "fi-hel-01")
	if err != nil {
		t.Fatal(err)
	}
	assertNodeStaticEqual(t, want, *got)
	if got.Capacity != want.Capacity || got.CurrentSessions != want.CurrentSessions {
		t.Fatalf("dynamic fields: %+v", got)
	}
	if !got.Health.Healthy || got.Health.SessionCount != 5 {
		t.Fatalf("health: %+v", got.Health)
	}
}

func TestSPKIPinPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pin := make([]byte, 32)
	for i := range pin {
		pin[i] = byte(i)
	}
	if err := st.CreateOrUpdateNodeConfig(ctx, model.NodeRegistryEntry{
		NodeID: "n1", LocationID: "loc", Enabled: true, SPKIPin: pin, ServerName: "s",
		LastSeen: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	got, err := st2.GetNode(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SPKIPin) != 32 {
		t.Fatalf("spki len=%d", len(got.SPKIPin))
	}
	for i := range pin {
		if got.SPKIPin[i] != pin[i] {
			t.Fatalf("spki mismatch at %d", i)
		}
	}
}

func TestHeartbeatDoesNotOverwriteNodeConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	pin := []byte{9, 8, 7, 6}
	before := model.NodeRegistryEntry{
		NodeID: "fi-hel-01", LocationID: "fi-hel", Country: "FI", City: "Helsinki",
		DisplayName: "Helsinki 01", Status: "healthy", Enabled: true, TestOnly: true,
		Draining: false, ProtocolVersion: 1, ServerVersion: "9.9.9",
		ServerName: "pin.example", SPKIPin: pin, Capacity: 100, CurrentSessions: 1,
		Health:   model.HealthInfo{Healthy: true, SessionCount: 1},
		LastSeen: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
	}
	if err := st.CreateOrUpdateNodeConfig(ctx, before); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpdateNodeHealth(ctx, "fi-hel-01", model.NodeHealth{
		Healthy: true, SessionCount: 42, CPUPercent: 80,
	}, 42, 150, now); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetNode(ctx, "fi-hel-01")
	if err != nil {
		t.Fatal(err)
	}
	assertNodeStaticEqual(t, before, *got)
	if got.CurrentSessions != 42 || got.Capacity != 150 {
		t.Fatalf("health fields not updated: sessions=%d capacity=%d", got.CurrentSessions, got.Capacity)
	}
	if !got.Health.Healthy || got.Health.SessionCount != 42 || got.Health.CPUPercent != 80 {
		t.Fatalf("health json: %+v", got.Health)
	}
	if !got.LastSeen.Equal(now) {
		t.Fatalf("last_seen=%v want %v", got.LastSeen, now)
	}
}

func assertNodeStaticEqual(t *testing.T, want, got model.NodeRegistryEntry) {
	t.Helper()
	if got.NodeID != want.NodeID || got.LocationID != want.LocationID ||
		got.Country != want.Country || got.City != want.City ||
		got.DisplayName != want.DisplayName || got.Status != want.Status ||
		got.Enabled != want.Enabled || got.TestOnly != want.TestOnly ||
		got.Draining != want.Draining || got.ProtocolVersion != want.ProtocolVersion ||
		got.ServerVersion != want.ServerVersion || got.ServerName != want.ServerName {
		t.Fatalf("static fields mismatch:\nwant=%+v\ngot=%+v", want, got)
	}
	if len(got.SPKIPin) != len(want.SPKIPin) {
		t.Fatalf("spki len want=%d got=%d", len(want.SPKIPin), len(got.SPKIPin))
	}
	for i := range want.SPKIPin {
		if got.SPKIPin[i] != want.SPKIPin[i] {
			t.Fatalf("spki mismatch")
		}
	}
}

func TestStoreRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	_ = st.Revoke(ctx, "license", "nyx_lic_1")
	ok, _ := st.IsRevoked(ctx, "license", "nyx_lic_1")
	if !ok {
		t.Fatal("expected revoked")
	}
}

func TestMigrationOldSchemaToNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy nodes table without SPKI / server_name / version columns.
	_, err = db.Exec(`
CREATE TABLE licenses (
  license_id TEXT PRIMARY KEY,
  plan TEXT NOT NULL,
  max_devices INTEGER NOT NULL DEFAULT 3,
  enabled INTEGER NOT NULL DEFAULT 1,
  revoked INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  locations_json TEXT
);
CREATE TABLE devices (
  device_id TEXT PRIMARY KEY,
  license_id TEXT NOT NULL,
  public_key BLOB,
  enabled INTEGER NOT NULL DEFAULT 1,
  revoked INTEGER NOT NULL DEFAULT 0,
  registered_at TEXT NOT NULL,
  last_seen TEXT
);
CREATE TABLE nodes (
  node_id TEXT PRIMARY KEY,
  location_id TEXT NOT NULL,
  country TEXT,
  city TEXT,
  display_name TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  test_only INTEGER NOT NULL DEFAULT 0,
  draining INTEGER NOT NULL DEFAULT 0,
  endpoints_json TEXT,
  capacity INTEGER DEFAULT 100,
  current_sessions INTEGER DEFAULT 0,
  health_json TEXT,
  last_seen TEXT
);
CREATE TABLE revocations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  ref_id TEXT NOT NULL,
  revoked_at TEXT NOT NULL
);
CREATE TABLE node_identities (
  node_id TEXT PRIMARY KEY,
  public_key BLOB NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1
);
INSERT INTO nodes (node_id, location_id, country, city, display_name, enabled, test_only, draining, endpoints_json, capacity, current_sessions, health_json, last_seen)
VALUES ('legacy-01', 'fi-hel', 'FI', 'Helsinki', 'Legacy', 1, 0, 0, '[]', 10, 0, '{}', '2020-01-01T00:00:00Z');
`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	got, err := st.GetNode(context.Background(), "legacy-01")
	if err != nil {
		t.Fatalf("GetNode after migrate: %v", err)
	}
	if got.LocationID != "fi-hel" || got.ServerName != "" {
		t.Fatalf("unexpected node after migrate: %+v", got)
	}
	pin := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	got.ServerName = "legacy.example"
	got.SPKIPin = pin
	got.ProtocolVersion = 1
	if err := st.CreateOrUpdateNodeConfig(context.Background(), *got); err != nil {
		t.Fatal(err)
	}
	again, err := st.GetNode(context.Background(), "legacy-01")
	if err != nil {
		t.Fatal(err)
	}
	if again.ServerName != "legacy.example" || len(again.SPKIPin) != len(pin) {
		t.Fatalf("spki/server_name not writable after migrate: %+v", again)
	}
	for i := range pin {
		if again.SPKIPin[i] != pin[i] {
			t.Fatal("spki mismatch")
		}
	}
}
