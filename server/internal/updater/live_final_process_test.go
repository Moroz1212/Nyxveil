package updater_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nyxveil/server/internal/updater"
)

func TestLiveFinalUpdateEmptyWorkingDirectory(t *testing.T) {
	runLiveFinalProcessFixture(t, fixtureOpts{crlfSums: false, fullInstall: true})
}

func TestLiveFinalUpdateDownloadsVersion(t *testing.T) {
	runLiveFinalProcessFixture(t, fixtureOpts{verifyChainOnly: true, assertNoCwdLeak: true})
}

func TestLiveFinalUpdateDownloadsBootstrap(t *testing.T) {
	runLiveFinalProcessFixture(t, fixtureOpts{verifyChainOnly: true, assertNoCwdLeak: true})
}

func TestLiveFinalUpdateHandlesLFChecksums(t *testing.T) {
	runLiveFinalProcessFixture(t, fixtureOpts{crlfSums: false, verifyChainOnly: true})
}

func TestLiveFinalUpdateHandlesCRLFChecksums(t *testing.T) {
	runLiveFinalProcessFixture(t, fixtureOpts{crlfSums: true, verifyChainOnly: true})
}

func TestMissingVersionFailsBeforeModification(t *testing.T) {
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "usr", "local", "sbin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ctl := filepath.Join(bin, "nyxveilctl")
	if err := os.WriteFile(ctl, []byte("old-ctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	srvDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srvDir, "release-manifest-linux-amd64.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	base := serveDir(t, srvDir)
	work := t.TempDir()
	script := filepath.Join(repoRootFromUpdaterTest(t), "scripts", "live-final-update.sh")
	dst := filepath.Join(work, "live-final-update.sh")
	copyFile(t, script, dst)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	cmd := exec.Command(bash, dst, "--base-url", base)
	cmd.Dir = work
	cmd.Env = liveFinalEnv(t, "NYXVEIL_BIN_DIR="+bin)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected failure, got success:\n%s", out)
	}
	if got := string(mustRead(t, ctl)); got != "old-ctl" {
		t.Fatalf("ctl modified: %q", got)
	}
}

func TestMissingBootstrapFailsBeforeModification(t *testing.T) {
	fx := buildSignedReleaseFixture(t, "1.1.4", false)
	os.Remove(filepath.Join(fx.dir, "bootstrap-cli-update.sh"))
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "usr", "local", "sbin")
	_ = os.MkdirAll(bin, 0o755)
	ctl := filepath.Join(bin, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("old-ctl-1.1.1"), 0o755)
	work := t.TempDir()
	dst := filepath.Join(work, "live-final-update.sh")
	copyFile(t, filepath.Join(repoRootFromUpdaterTest(t), "scripts", "live-final-update.sh"), dst)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	cmd := exec.Command(bash, dst, "--base-url", fx.base)
	cmd.Dir = work
	cmd.Env = liveFinalEnv(t, "NYXVEIL_BIN_DIR="+bin, "NYXVEIL_UPDATE_PUB_HEX="+fx.pubHex)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected missing bootstrap failure:\n%s", out)
	}
	if got := string(mustRead(t, ctl)); got != "old-ctl-1.1.1" {
		t.Fatalf("ctl modified: %q", got)
	}
}

func TestTamperedBootstrapFails(t *testing.T) {
	fx := buildSignedReleaseFixture(t, "1.1.4", false)
	evil := []byte("#!/bin/bash\necho evil-no-pubkey\n")
	if err := os.WriteFile(filepath.Join(fx.dir, "bootstrap-cli-update.sh"), evil, 0o755); err != nil {
		t.Fatal(err)
	}
	rewriteSumsForFile(t, fx.dir, "bootstrap-cli-update.sh", evil)
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "usr", "local", "sbin")
	_ = os.MkdirAll(bin, 0o755)
	ctl := filepath.Join(bin, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("old-ctl-1.1.1"), 0o755)
	work := t.TempDir()
	dst := filepath.Join(work, "live-final-update.sh")
	copyFile(t, filepath.Join(repoRootFromUpdaterTest(t), "scripts", "live-final-update.sh"), dst)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	cmd := exec.Command(bash, dst, "--base-url", fx.base, "--verify-chain")
	cmd.Dir = work
	cmd.Env = liveFinalEnv(t, "NYXVEIL_BIN_DIR="+bin, "NYXVEIL_UPDATE_PUB_HEX="+fx.pubHex)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected tampered bootstrap failure:\n%s", out)
	}
	if got := string(mustRead(t, ctl)); got != "old-ctl-1.1.1" {
		t.Fatalf("ctl modified: %q", got)
	}
}

func TestTamperedCtlFails(t *testing.T) {
	fx := buildSignedReleaseFixture(t, "1.1.4", false)
	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	if err := os.WriteFile(filepath.Join(fx.dir, "nyxveilctl-linux-"+arch), []byte("tampered-ctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "usr", "local", "sbin")
	_ = os.MkdirAll(bin, 0o755)
	ctl := filepath.Join(bin, "nyxveilctl")
	_ = os.WriteFile(ctl, []byte("old-ctl-1.1.1"), 0o755)
	work := t.TempDir()
	dst := filepath.Join(work, "live-final-update.sh")
	copyFile(t, filepath.Join(repoRootFromUpdaterTest(t), "scripts", "live-final-update.sh"), dst)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash required")
	}
	cmd := exec.Command(bash, dst, "--base-url", fx.base)
	cmd.Dir = work
	cmd.Env = liveFinalEnv(t,
		"NYXVEIL_BIN_DIR="+bin,
		"NYXVEIL_SHARE_DIR="+filepath.Join(prefix, "usr", "local", "share", "nyxveil"),
		"NYXVEIL_STATE_DIR="+filepath.Join(prefix, "var", "lib", "nyxveil"),
		"NYXVEIL_UPDATE_PUB_HEX="+fx.pubHex,
	)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected tampered ctl failure:\n%s", out)
	}
	if got := string(mustRead(t, ctl)); got != "old-ctl-1.1.1" {
		t.Fatalf("ctl modified: %q", got)
	}
}

type fixtureOpts struct {
	crlfSums        bool
	verifyChainOnly bool
	fullInstall     bool
	assertNoCwdLeak bool
}

type signedFixture struct {
	dir    string
	base   string
	pubHex string
}

func manifestToolEnv(t *testing.T) string {
	t.Helper()
	root := filepath.ToSlash(repoRootFromUpdaterTest(t))
	// -C keeps module context when live-final runs from an empty workdir.
	return "go run -C " + root + " ./scripts/manifest-tool.go"
}

func liveFinalEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	env := append(os.Environ(),
		"NYXVEIL_SKIP_ROOT=1",
		"NYXVEIL_MANIFEST_TOOL="+manifestToolEnv(t),
	)
	return append(env, extra...)
}

func runLiveFinalProcessFixture(t *testing.T, opts fixtureOpts) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash required")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl required")
	}
	fx := buildSignedReleaseFixture(t, "1.1.4", opts.crlfSums)
	work := t.TempDir()
	dst := filepath.Join(work, "live-final-update.sh")
	copyFile(t, filepath.Join(repoRootFromUpdaterTest(t), "scripts", "live-final-update.sh"), dst)
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || entries[0].Name() != "live-final-update.sh" {
		t.Fatalf("empty workdir invariant broken")
	}

	args := []string{dst, "--base-url", fx.base}
	env := liveFinalEnv(t, "NYXVEIL_UPDATE_PUB_HEX="+fx.pubHex)
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "usr", "local", "sbin")
	share := filepath.Join(prefix, "usr", "local", "share", "nyxveil")
	state := filepath.Join(prefix, "var", "lib", "nyxveil")
	_ = os.MkdirAll(bin, 0o755)
	_ = os.MkdirAll(filepath.Join(share, "scripts"), 0o755)
	_ = os.MkdirAll(state, 0o755)
	_ = os.WriteFile(filepath.Join(bin, "nyxveil-server"), []byte("old-server-1.1.1"), 0o755)
	_ = os.WriteFile(filepath.Join(bin, "nyxveilctl"), []byte("old-ctl-1.1.1"), 0o755)
	env = append(env,
		"NYXVEIL_BIN_DIR="+bin,
		"NYXVEIL_SHARE_DIR="+share,
		"NYXVEIL_STATE_DIR="+state,
		"NYXVEIL_SKIP_GATE=1",
	)
	if opts.verifyChainOnly {
		args = append(args, "--verify-chain")
	}

	cmd := exec.Command("bash", args...)
	cmd.Dir = work
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("live-final failed: %v\n%s", err, out)
	}
	if opts.verifyChainOnly && !strings.Contains(string(out), "LIVE_FINAL_UPDATE_CONSUMER=PASS") {
		t.Fatalf("missing consumer pass:\n%s", out)
	}
	if opts.assertNoCwdLeak {
		if _, err := os.Stat(filepath.Join(work, "VERSION")); err == nil {
			t.Fatal("VERSION leaked into caller cwd")
		}
		if _, err := os.Stat(filepath.Join(work, "bootstrap-cli-update.sh")); err == nil {
			t.Fatal("bootstrap leaked into caller cwd")
		}
	}
	if opts.fullInstall {
		for _, rel := range []string{
			"nyxveil-server",
			"nyxveilctl",
			"nyxveil-catalog-verify",
		} {
			if _, err := os.Stat(filepath.Join(bin, rel)); err != nil {
				t.Fatalf("missing %s: %v\nout=%s", rel, err, out)
			}
		}
		if _, err := os.Stat(filepath.Join(share, "scripts", "production-gate.sh")); err != nil {
			t.Fatalf("missing gate: %v\nout=%s", err, out)
		}
		if got := strings.TrimSpace(string(mustRead(t, filepath.Join(share, "VERSION")))); got != "1.1.4" {
			t.Fatalf("share VERSION=%q", got)
		}
	}
}

func buildSignedReleaseFixture(t *testing.T, version string, crlf bool) signedFixture {
	t.Helper()
	dir := t.TempDir()
	root := repoRootFromUpdaterTest(t)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(pub)

	payloads := map[string][]byte{
		"nyxveil-server":         []byte("#!/usr/bin/env bash\necho server-" + version + "\n"),
		"nyxveilctl":             buildStubCtl(t),
		"nyxveil-catalog-verify": []byte("#!/usr/bin/env bash\necho catalog\n"),
		"production-gate":        []byte("#!/usr/bin/env bash\necho RESULT=PASS\n"),
		"share-version":          []byte(version + "\n"),
		"share-third-party-core": []byte("frozen-core\n"),
	}
	for _, arch := range []string{"amd64", "arm64"} {
		writeExec(t, filepath.Join(dir, "nyxveil-server-linux-"+arch), payloads["nyxveil-server"])
		writeExec(t, filepath.Join(dir, "nyxveilctl-linux-"+arch), payloads["nyxveilctl"])
		writeExec(t, filepath.Join(dir, "nyxveil-catalog-verify-linux-"+arch), payloads["nyxveil-catalog-verify"])
	}
	writeExec(t, filepath.Join(dir, "production-gate.sh"), payloads["production-gate"])
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), payloads["share-version"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "THIRD_PARTY_CORE.md"), payloads["share-third-party-core"], 0o644); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(root, "scripts", "bootstrap-cli-update.sh"), filepath.Join(dir, "bootstrap-cli-update.sh"))
	copyFile(t, filepath.Join(root, "scripts", "live-final-update.sh"), filepath.Join(dir, "live-final-update.sh"))
	rewritePubHex(t, filepath.Join(dir, "bootstrap-cli-update.sh"), pubHex)
	rewritePubHex(t, filepath.Join(dir, "live-final-update.sh"), pubHex)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	srv := &http.Server{Handler: http.FileServer(http.Dir(dir))}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Ephemeral key: tests must not require production .secrets on developer hosts.
	for _, arch := range []string{"amd64", "arm64"} {
		writeTestManifest(t, dir, version, arch, base, priv)
	}
	writeReleaseSums(t, dir, crlf)
	return signedFixture{dir: dir, base: base, pubHex: pubHex}
}

const productionPubHex = "caf921521e213cb1bcdc2f9df4816c2ecd43222b23a47d6f869672e6ab0e79af"

func rewritePubHex(t *testing.T, path, pubHex string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.ReplaceAll(string(b), productionPubHex, pubHex)
	if updated == string(b) {
		t.Fatalf("%s missing production PUB_HEX to rewrite", path)
	}
	if err := os.WriteFile(path, []byte(updated), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeTestManifest(t *testing.T, dir, version, arch, base string, priv ed25519.PrivateKey) {
	t.Helper()
	type item struct {
		name, file, dest, mode string
	}
	items := []item{
		{"nyxveil-server", "nyxveil-server-linux-" + arch, "/usr/local/sbin/nyxveil-server", "0755"},
		{"nyxveilctl", "nyxveilctl-linux-" + arch, "/usr/local/sbin/nyxveilctl", "0755"},
		{"nyxveil-catalog-verify", "nyxveil-catalog-verify-linux-" + arch, "/usr/local/sbin/nyxveil-catalog-verify", "0755"},
		{"production-gate", "production-gate.sh", "/usr/local/share/nyxveil/scripts/production-gate.sh", "0755"},
		{"share-version", "VERSION", "/usr/local/share/nyxveil/VERSION", "0644"},
		{"share-third-party-core", "THIRD_PARTY_CORE.md", "/usr/local/share/nyxveil/THIRD_PARTY_CORE.md", "0644"},
	}
	m := &updater.Manifest{
		Version: version, Arch: "linux/" + arch, MinCore: "1.0.0", MinProtocol: 1,
	}
	for _, it := range items {
		sum := shaHex(mustRead(t, filepath.Join(dir, it.file)))
		m.Assets = append(m.Assets, updater.Asset{
			Name: it.name, SHA256: sum, URL: base + "/" + it.file,
			Destination: it.dest, Mode: it.mode, Required: true,
		})
	}
	updater.SignManifest(m, priv)
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	out := filepath.Join(dir, "release-manifest-linux-"+arch+".json")
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := updater.ParseManifest(raw, priv.Public().(ed25519.PublicKey)); err != nil {
		t.Fatalf("fixture self-verify: %v", err)
	}
}

func stubCtlScript() []byte {
	// Built as a tiny Go helper for Windows/Linux fixture hosts without python.
	return nil
}

func buildStubCtl(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	code := `package main
import (
  "crypto/sha256"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "io"
  "net/http"
  "os"
  "path/filepath"
  "strconv"
  "strings"
)
type asset struct{ Name, SHA256, URL, Mode string }
type manifest struct{ Version string; Assets []asset }
func loadManifest(src string) []byte {
  if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
    resp, err := http.Get(src)
    if err != nil { panic(err) }
    defer resp.Body.Close()
    b, err := io.ReadAll(resp.Body)
    if err != nil { panic(err) }
    return b
  }
  b, err := os.ReadFile(src)
  if err != nil { panic(err) }
  return b
}
func main() {
  if len(os.Args) < 2 || os.Args[1] != "update" {
    fmt.Println("stub-ctl", os.Args)
    return
  }
  raw := loadManifest(os.Args[2])
  var m manifest
  if err := json.Unmarshal(raw, &m); err != nil { panic(err) }
  bin := os.Getenv("NYXVEIL_BIN_DIR")
  if bin == "" { bin = "/usr/local/sbin" }
  share := os.Getenv("NYXVEIL_SHARE_DIR")
  if share == "" { share = "/usr/local/share/nyxveil" }
  dest := map[string]string{
    "nyxveil-server": filepath.Join(bin, "nyxveil-server"),
    "nyxveilctl": filepath.Join(bin, "nyxveilctl"),
    "nyxveil-catalog-verify": filepath.Join(bin, "nyxveil-catalog-verify"),
    "production-gate": filepath.Join(share, "scripts", "production-gate.sh"),
    "share-version": filepath.Join(share, "VERSION"),
    "share-third-party-core": filepath.Join(share, "THIRD_PARTY_CORE.md"),
  }
  for _, a := range m.Assets {
    p, ok := dest[a.Name]
    if !ok { continue }
    if a.Name == "nyxveilctl" {
      // Windows cannot overwrite the running stub executable; live-final already
      // installed this ctl binary before invoking update.
      if exe, err := os.Executable(); err == nil {
        if filepath.Clean(exe) == filepath.Clean(p) {
          continue
        }
      }
    }
    resp, err := http.Get(a.URL)
    if err != nil { panic(err) }
    data, err := io.ReadAll(resp.Body)
    resp.Body.Close()
    if err != nil { panic(err) }
    sum := sha256.Sum256(data)
    if hex.EncodeToString(sum[:]) != a.SHA256 { panic("hash mismatch "+a.Name) }
    if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { panic(err) }
    mode := os.FileMode(0o755)
    if a.Mode != "" {
      if n, err := strconv.ParseUint(a.Mode, 8, 32); err == nil { mode = os.FileMode(n) }
    }
    tmp := p + ".new"
    if err := os.WriteFile(tmp, data, mode); err != nil { panic(err) }
    if err := os.Rename(tmp, p); err != nil {
      _ = os.Remove(p)
      if err2 := os.Rename(tmp, p); err2 != nil { panic(err2) }
    }
  }
  fmt.Println("stub-ctl: updated to", m.Version)
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "stubctl")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build stub ctl: %v\n%s", err, b)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeReleaseSums(t *testing.T, dir string, crlf bool) {
	t.Helper()
	names := []string{
		"nyxveil-server-linux-amd64", "nyxveilctl-linux-amd64", "nyxveil-catalog-verify-linux-amd64",
		"nyxveil-server-linux-arm64", "nyxveilctl-linux-arm64", "nyxveil-catalog-verify-linux-arm64",
		"production-gate.sh", "VERSION", "THIRD_PARTY_CORE.md",
		"release-manifest-linux-amd64.json", "release-manifest-linux-arm64.json",
		"bootstrap-cli-update.sh", "live-final-update.sh",
	}
	var b strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(mustRead(t, filepath.Join(dir, name)))
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString("  ")
		b.WriteString(name)
		if crlf {
			b.WriteString("\r\n")
		} else {
			b.WriteString("\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rewriteSumsForFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	path := filepath.Join(dir, "SHA256SUMS")
	raw := string(mustRead(t, path))
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.HasSuffix(line, " "+name) || strings.HasSuffix(line, "*"+name) {
			lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func serveDir(t *testing.T, dir string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(dir)), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

func writeExec(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// Normalize CRLF for shell scripts copied onto Windows.
	if strings.HasSuffix(src, ".sh") {
		b = []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
		b = []byte(strings.ReplaceAll(string(b), "\r", ""))
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatal(err)
	}
}
