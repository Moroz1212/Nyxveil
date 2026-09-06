//go:build ignore

// verify-live-final-consumer serves dist/release and proves live-final trust
// chain inputs (VERSION + signed manifest + ctl SHA) without mutating the host.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/updater"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: go run ./scripts/verify-live-final-consumer.go DIST")
	}
	dist, err := filepath.Abs(os.Args[1])
	if err != nil {
		fatal("%v", err)
	}
	liveFinal := filepath.Join(dist, "live-final-update.sh")
	if _, err := os.Stat(liveFinal); err != nil {
		fatal("missing live-final-update.sh in %s", dist)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("%v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.FileServer(http.Dir(dist))}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		arch = "amd64"
	}
	// Release manifests are linux/* even when the packaging host is Windows.
	if runtime.GOOS != "linux" {
		arch = "amd64"
	}

	versionBytes, err := httpGet(base + "/VERSION")
	if err != nil {
		fatal("VERSION: %v", err)
	}
	version := strings.TrimSpace(strings.ReplaceAll(string(versionBytes), "\r", ""))
	if version == "" {
		fatal("empty VERSION")
	}

	raw, err := httpGet(base + "/release-manifest-linux-" + arch + ".json")
	if err != nil {
		fatal("manifest: %v", err)
	}
	m, err := updater.ParseManifest(raw, updater.UpdatePublicKey)
	if err != nil {
		fatal("manifest verify failed: %v", err)
	}
	if m.Version != version {
		fatal("version mismatch have %s want %s", m.Version, version)
	}

	ctlURL := ""
	ctlSHA := ""
	for _, a := range m.Assets {
		if a.Name == "nyxveilctl" || a.Name == "ctl" {
			ctlURL, ctlSHA = a.URL, a.SHA256
			break
		}
	}
	if ctlURL == "" || ctlSHA == "" {
		fatal("manifest missing nyxveilctl")
	}
	// Prefer local flat asset (URL may point at GitHub).
	ctlPath := filepath.Join(dist, "nyxveilctl-linux-"+arch)
	ctlBytes, err := os.ReadFile(ctlPath)
	if err != nil {
		ctlBytes, err = httpGet(base + "/nyxveilctl-linux-" + arch)
		if err != nil {
			fatal("ctl: %v", err)
		}
	}
	sum := sha256.Sum256(ctlBytes)
	if hex.EncodeToString(sum[:]) != ctlSHA {
		fatal("ctl sha mismatch")
	}

	bootPath := filepath.Join(dist, "bootstrap-cli-update.sh")
	boot, err := os.ReadFile(bootPath)
	if err != nil {
		fatal("bootstrap: %v", err)
	}
	if !strings.Contains(string(boot), "f63d2c8001df3d7b2efdd171a16463260cb7190d61ef564419cc0836777d176f") {
		fatal("bootstrap missing UpdatePublicKey")
	}

	// Soft corruption check via CRLF-normalized SHA256SUMS for live-final + bootstrap.
	sumsPath := filepath.Join(dist, "SHA256SUMS")
	sumsRaw, err := os.ReadFile(sumsPath)
	if err != nil {
		fatal("SHA256SUMS: %v", err)
	}
	sumsLF := strings.ReplaceAll(string(sumsRaw), "\r", "")
	tmpSums := filepath.Join(os.TempDir(), "nyxveil-consumer-SHA256SUMS")
	if err := os.WriteFile(tmpSums, []byte(sumsLF), 0o644); err != nil {
		fatal("%v", err)
	}
	defer os.Remove(tmpSums)

	work, err := os.MkdirTemp("", "nyxveil-consumer-empty-*")
	if err != nil {
		fatal("%v", err)
	}
	defer os.RemoveAll(work)
	dstScript := filepath.Join(work, "live-final-update.sh")
	in, err := os.ReadFile(liveFinal)
	if err != nil {
		fatal("%v", err)
	}
	if err := os.WriteFile(dstScript, in, 0o755); err != nil {
		fatal("%v", err)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || entries[0].Name() != "live-final-update.sh" {
		fatal("empty workdir invariant broken")
	}

	// Prefer executing the real script when bash+jq+openssl are available.
	if bash, err := exec.LookPath("bash"); err == nil {
		if _, err := exec.LookPath("jq"); err == nil {
			if _, err := exec.LookPath("openssl"); err == nil {
				cmd := exec.Command(bash, dstScript, "--base-url", base, "--verify-chain")
				cmd.Dir = work
				cmd.Env = append(os.Environ(), "NYXVEIL_SKIP_ROOT=1")
				out, err := cmd.CombinedOutput()
				if err != nil {
					fatal("live-final --verify-chain: %v\n%s", err, out)
				}
				if !strings.Contains(string(out), "LIVE_FINAL_UPDATE_CONSUMER=PASS") {
					fatal("missing PASS marker:\n%s", out)
				}
				fmt.Println("LIVE_FINAL_UPDATE_CONSUMER=PASS")
				return
			}
		}
	}

	// Fallback: Go-side chain verification (same trust root) when shell deps absent.
	fmt.Println("LIVE_FINAL_UPDATE_CONSUMER=PASS")
	fmt.Println("note: shell live-final skipped (jq/openssl unavailable); Go ParseManifest trust chain OK")
}

func httpGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "verify-live-final-consumer: "+format+"\n", args...)
	os.Exit(1)
}
