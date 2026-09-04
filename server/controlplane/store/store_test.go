package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyxveil/nvp/controlplane/model"
	"github.com/nyxveil/nvp/controlplane/store"
)

func TestStoreLicenseAndDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

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

func TestStoreNodes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

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

func TestStoreRevocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	_ = st.Revoke(ctx, "license", "nyx_lic_1")
	ok, _ := st.IsRevoked(ctx, "license", "nyx_lic_1")
	if !ok {
		t.Fatal("expected revoked")
	}
}
