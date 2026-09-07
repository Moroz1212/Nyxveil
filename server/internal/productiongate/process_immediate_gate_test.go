package productiongate_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/health"
	"github.com/nyxveil/server/internal/productiongate"
	"github.com/nyxveil/server/internal/version"
)

// TestProcessImmediatePostUpdateGateCPReachable reproduces the live contradiction:
// updater post-check sees management_plane_connected=true, then the installed
// production-gate.sh is run immediately and must PASS control_plane_reachable
// using the same runtime cp_connected truth (not curl /health).
func TestProcessImmediatePostUpdateGateCPReachable(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required for production-gate.sh")
	}
	if out, err := exec.Command("python3", "-c", "import json; print(1)").CombinedOutput(); err != nil || !strings.Contains(string(out), "1") {
		t.Skipf("python3 is not usable for gate JSON helpers: %v %s", err, out)
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl required for production-gate.sh TLS checks")
	}

	root := findServerRoot(t)
	serverBin, ctlBin := buildServerAndCtl(t, root)
	gateSrc := filepath.Join(root, "scripts", "production-gate.sh")

	var hbDelay atomic.Bool
	hbDelay.Store(true)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keysJSON := `{"issuer":"nyxveil-control-plane","keys":{"k1":"` + base64.StdEncoding.EncodeToString(pub) + `"},"updated_at":1}`

	cp, caPEM := startAuthenticatedHTTPSCP(t, &hbDelay, keysJSON)
	defer cp.Close()

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	etc := filepath.Join(dir, "etc")
	share := filepath.Join(dir, "share", "nyxveil")
	scripts := filepath.Join(share, "scripts")
	for _, d := range []string{state, etc, scripts} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	caFile := filepath.Join(state, "test-cp-ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(etc, "server.json")
	cfg := map[string]any{
		"control_plane_url": cp.URL,
		"pinned_ca_file":    caFile,
		"node_id":           "gate-node-1",
		"location_id":       "test-loc",
		"display_name":      "gate-test",
		"public_host":       "127.0.0.1",
		"tls_listen":        "127.0.0.1:0",
		"quic_listen":       "127.0.0.1:0",
		"heartbeat_sec":     1,
	}
	writeJSON(t, cfgPath, cfg)
	if err := os.WriteFile(filepath.Join(state, "ticket-keys.json"), []byte(keysJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSelfSignedTLS(t, filepath.Join(state, "tls.crt"), filepath.Join(state, "tls.key"))
	if err := os.WriteFile(filepath.Join(share, "VERSION"), []byte(version.ServerVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := "Frozen Core SHA256: 7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b\n"
	if err := os.WriteFile(filepath.Join(share, "THIRD_PARTY_CORE.md"), []byte(third), 0o644); err != nil {
		t.Fatal(err)
	}
	installedGate := filepath.Join(scripts, "production-gate.sh")
	rawGate, err := os.ReadFile(gateSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedGate, rawGate, 0o755); err != nil {
		t.Fatal(err)
	}

	nodeHTTP, stopNode := startRuntimeNode(t, cfgPath, state, cp.URL, caFile)
	defer stopNode()

	waitRuntimeCP(t, nodeHTTP, 20*time.Second)
	preRaw := mustGET(t, nodeHTTP+"/status")
	pre, err := health.ParseStatusJSON(preRaw)
	if err != nil {
		t.Fatal(err)
	}
	preBase := health.CaptureBaseline(pre)
	if !preBase.CPConnected {
		t.Fatalf("pre baseline cp_connected=false status=%s", preRaw)
	}

	stopNode()
	hbDelay.Store(true)
	time.Sleep(200 * time.Millisecond)
	nodeHTTP, stopNode = startRuntimeNode(t, cfgPath, state, cp.URL, caFile)
	defer stopNode()

	var postRes health.UpdateResult
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		raw := mustGET(t, nodeHTTP+"/status")
		st, err := health.ParseStatusJSON(raw)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		postRes = health.EvaluatePostUpdate(preBase, st)
		if postRes.OK && postRes.ManagementPlaneConnected {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !postRes.OK || !postRes.ManagementPlaneConnected {
		t.Fatalf("updater post-check failed: %+v", postRes)
	}
	if err := productiongate.UpdaterAndGateAgree(true, mustGET(t, nodeHTTP+"/status")); err != nil {
		t.Fatal(err)
	}

	hbDelay.Store(false)
	cmd := exec.Command("bash", installedGate)
	cmd.Env = append(os.Environ(),
		"GATE_MODE=live",
		"GATE_STOP_AFTER_CP=1",
		"GATE_CP_WAIT_SEC=30",
		"GATE_CP_STABLE_SAMPLES=2",
		"GATE_CP_POLL_INTERVAL_SEC=1",
		"GATE_UPDATER_POSTCHECK=management_plane_connected=true",
		"NYXVEIL_CTL="+ctlBin,
		"NYXVEIL_SERVER="+serverBin,
		"NYXVEIL_SERVER_BINARY="+serverBin,
		"NYXVEIL_CONTROL_HTTP="+nodeHTTP,
		"NYXVEIL_CONFIG="+cfgPath,
		"NYXVEIL_STATE_DIR="+state,
		"NYXVEIL_SHARE_DIR="+share,
		"NYXVEIL_SHARE_VERSION="+filepath.Join(share, "VERSION"),
		"NYXVEIL_EXPECTED_VERSION="+version.ServerVersion,
		"NYXVEIL_TLS_CERT="+filepath.Join(state, "tls.crt"),
		"NYXVEIL_TLS_KEY="+filepath.Join(state, "tls.key"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("installed production-gate immediately after update: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "RESULT=PASS") {
		t.Fatalf("expected RESULT=PASS, got:\n%s", out)
	}
	if strings.Contains(string(out), "failed_gate=control_plane_reachable") {
		t.Fatalf("control_plane_reachable must not fail:\n%s", out)
	}
}

func TestGateAndDaemonUseSameTLSModeField(t *testing.T) {
	raw := []byte(`{"cp_connected":true,"cp_tls_mode":"SystemTrust","cp_tls_status":"ok","cp_url":"https://cp.nyxveil.ru:18443"}`)
	st, err := health.ParseStatusJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if st.CPTLSMode != "SystemTrust" {
		t.Fatalf("tls mode=%q", st.CPTLSMode)
	}
	if st.CPURL != "https://cp.nyxveil.ru:18443" {
		t.Fatal(st.CPURL)
	}
}

func buildServerAndCtl(t *testing.T, root string) (serverBin, ctlBin string) {
	t.Helper()
	dir := t.TempDir()
	serverBin = filepath.Join(dir, "nyxveil-server")
	ctlBin = filepath.Join(dir, "nyxveilctl")
	if runtime.GOOS == "windows" {
		serverBin += ".exe"
		ctlBin += ".exe"
	}
	build := func(out, pkg string) {
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, b)
		}
	}
	build(serverBin, "./cmd/nyxveil-server")
	build(ctlBin, "./cmd/nyxveilctl")
	return serverBin, ctlBin
}

type cpServer struct {
	URL string
	Srv *http.Server
	ln  net.Listener
}

func (c *cpServer) Close() {
	_ = c.Srv.Close()
	_ = c.ln.Close()
}

func startAuthenticatedHTTPSCP(t *testing.T, delay *atomic.Bool, keysJSON string) (*cpServer, []byte) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "nyxveil-test-cp-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leafDER, caDER},
			PrivateKey:  leafKey,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "diagnostic curl should not matter", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/api/v1/nodes/", func(w http.ResponseWriter, r *http.Request) {
		if delay != nil && delay.Load() {
			time.Sleep(400 * time.Millisecond)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true, "status": "ok", "config_version": 1,
		})
	})
	mux.HandleFunc("/api/v1/revocation", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"revoked_jtis":[],"revoked_licenses":[],"revoked_devices":[],"updated_at":1}`))
	})
	mux.HandleFunc("/api/v1/node/ticket-keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(keysJSON))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	url := fmt.Sprintf("https://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	return &cpServer{URL: url, Srv: srv, ln: ln}, caPEM
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSelfSignedTLS(t *testing.T, certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(9),
		Subject:      pkix.Name{CommonName: "node-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustGET(t *testing.T, url string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func waitRuntimeCP(t *testing.T, controlHTTP string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := http.Get(controlHTTP + "/status")
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var st struct {
			CPConnected bool `json:"cp_connected"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if st.CPConnected {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("timeout waiting for runtime cp_connected")
}
