package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/controlplane"
	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/localconfig"
)

func TestRollbackTLSFailureIsTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NYXVEIL_STATE_DIR", dir)
	t.Setenv("NYXVEIL_CONTROL_HTTP", "http://127.0.0.1:9")
	for _, name := range []string{"server", "ctl", "nyxveil-server.prev", "nyxveilctl.prev"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	old := rollbackEnforceTLS
	t.Cleanup(func() { rollbackEnforceTLS = old })
	rollbackEnforceTLS = func(string) error { return fmt.Errorf("TLS ownership denied") }
	tx := &updateTransaction{ID: "tls-fault", ServerPath: filepath.Join(dir, "server"), CtlPath: filepath.Join(dir, "ctl"), CtlPrev: filepath.Join(dir, "nyxveilctl.prev"), PreviousVersion: "1.1.9", FailureReason: "health timeout"}
	if err := rollbackAcrossHandoff(tx); err == nil {
		t.Fatal("TLS failure ignored")
	}
	loaded, err := loadUpdateTransaction(tx.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != txPhaseRollbackFailed || !strings.Contains(loaded.FailureReason, "health timeout") || !strings.Contains(loaded.FailureReason, "TLS ownership denied") {
		t.Fatalf("failure cause lost: %+v", loaded)
	}
}

func TestLegacy119JournalLifecycleAndOwnership(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NYXVEIL_STATE_DIR", dir)
	cfg := controlplane.NodeConfig{NodeID: "n1", ConfigVersion: 9, Enabled: true, Draining: true}
	if err := localconfig.SaveApplied(filepath.Join(dir, "applied-config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	old := ctlStatusJSON
	t.Cleanup(func() { ctlStatusJSON = old })
	ctlStatusJSON = func() ([]byte, error) {
		return json.Marshal(health.Status{NodeID: "n1", ConfigVersion: 9, Draining: true})
	}
	tx := &updateTransaction{ID: "legacy", LegacyParent: true, PreBaseline: health.Baseline{Accepting: false}, ProcessCLIAtStart: "1.1.9"}
	if err := restoreLegacyLifecycle(tx); err != nil {
		t.Fatal(err)
	}
	if !tx.PreBaseline.IntentionallyStopped() {
		t.Fatal("legacy lifecycle not recovered")
	}
	for _, phase := range []string{txPhaseRolledBackHealthy, txPhaseRollbackFailed} {
		tx.Phase = phase
		if err := writeUpdateTransaction(tx); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(txPath(tx.ID))
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if wire.Phase != "rolled_back" && wire.Phase != "rolling_back" {
			t.Fatal("1.1.9 parent would double rollback")
		}
		loaded, err := loadUpdateTransaction(tx.ID)
		if err != nil || loaded.Phase != phase {
			t.Fatalf("terminal outcome lost: %+v %v", loaded, err)
		}
	}
	tx.PreBaseline = health.Baseline{}
	ctlStatusJSON = func() ([]byte, error) {
		return json.Marshal(health.Status{NodeID: "other", ConfigVersion: 9, Draining: true})
	}
	if restoreLegacyLifecycle(tx) == nil {
		t.Fatal("mismatched identity accepted")
	}
}

func TestTLSIntegrityDuringDrain(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"node.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateTLSMaterial(certPath, keyPath, "node.example"); err != nil {
		t.Fatal(err)
	}
	if validateTLSMaterial(certPath, keyPath, "wrong.example") == nil {
		t.Fatal("bad SAN accepted")
	}
	if err := os.WriteFile(keyPath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateTLSMaterial(certPath, keyPath, "node.example") == nil {
		t.Fatal("damaged key accepted while drained")
	}
}
