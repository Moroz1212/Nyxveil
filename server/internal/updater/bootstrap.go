package updater

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
)

// BootstrapCLIOpts replaces ONLY nyxveilctl from a signed release — never touches
// nyxveil-server, TLS, server.json, nftables, or node identity.
//
// Use this to escape legacy broken updaters (server ≤1.0.4) before running a full update.
type BootstrapCLIOpts struct {
	ManifestURL string // required unless constructed via ManifestURLForTag
	WantVersion string // if set, reject manifests with a different version
	WantArch    string // empty = ArchString()
	CtlPath     string // install destination; default /usr/local/sbin/nyxveilctl
	PublicKey   ed25519.PublicKey
	HTTP        *http.Client

	// Test hooks.
	Download      func(url, dest string) error
	AtomicInstall func(src, dest string) error
}

// BootstrapCLIResult is operator-safe (no secrets).
type BootstrapCLIResult struct {
	Version     string `json:"version"`
	Arch        string `json:"arch"`
	CtlPath     string `json:"ctl_path"`
	PreviousSHA string `json:"previous_sha256,omitempty"`
	NewSHA      string `json:"new_sha256"`
	Replaced    bool   `json:"replaced"`
}

// ManifestURLForVersion builds the arch-aware GitHub Releases manifest URL for server-vVERSION.
func ManifestURLForVersion(version string) string {
	v := strings.TrimSpace(strings.TrimPrefix(version, "v"))
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		arch = "amd64"
	}
	return "https://github.com/Moroz1212/Nyxveil/releases/download/server-v" + v +
		"/release-manifest-linux-" + arch + ".json"
}

// BootstrapCLI downloads and atomically installs ONLY the nyxveilctl asset from a
// signed multi-asset release manifest. Fail-closed on bad signature/hash/arch/version.
func BootstrapCLI(opts BootstrapCLIOpts) (*BootstrapCLIResult, error) {
	if opts.ManifestURL == "" {
		return nil, fmt.Errorf("updater: bootstrap-cli requires manifest URL")
	}
	pub := opts.PublicKey
	if pub == nil {
		pub = UpdatePublicKey
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Minute}
	}
	ctlPath := opts.CtlPath
	if ctlPath == "" {
		ctlPath = "/usr/local/sbin/nyxveilctl"
	}
	wantArch := opts.WantArch
	if wantArch == "" {
		wantArch = ArchString()
	}

	resp, err := httpClient.Get(opts.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("updater: bootstrap-cli download manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: bootstrap-cli manifest HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(raw, pub)
	if err != nil {
		return nil, fmt.Errorf("updater: bootstrap-cli manifest verify failed: %w", err)
	}
	if m.Arch != "" && m.Arch != wantArch {
		return nil, fmt.Errorf("updater: bootstrap-cli arch mismatch have %s want %s", m.Arch, wantArch)
	}
	if opts.WantVersion != "" && strings.TrimSpace(m.Version) != strings.TrimSpace(opts.WantVersion) {
		return nil, fmt.Errorf("updater: bootstrap-cli version mismatch have %s want %s", m.Version, opts.WantVersion)
	}

	var ctlAsset *Asset
	for i := range m.Assets {
		a := &m.Assets[i]
		if a.Name == "nyxveilctl" || a.Name == "ctl" {
			ctlAsset = a
			break
		}
	}
	if ctlAsset == nil {
		return nil, fmt.Errorf("updater: bootstrap-cli manifest missing nyxveilctl asset")
	}

	prevSHA := ""
	if sum, err := fileSHA256(ctlPath); err == nil {
		prevSHA = sum
	}

	tmpDir, err := os.MkdirTemp("", "nyxveil-bootstrap-cli-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	tmpBin := filepath.Join(tmpDir, "nyxveilctl.new")

	dl := opts.Download
	if dl == nil {
		dl = func(url, dest string) error {
			u := &Updater{HTTP: httpClient}
			return u.download(url, dest)
		}
	}
	if err := dl(ctlAsset.URL, tmpBin); err != nil {
		return nil, fmt.Errorf("updater: bootstrap-cli download ctl: %w", err)
	}
	sum, err := fileSHA256(tmpBin)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(sum, strings.TrimSpace(ctlAsset.SHA256)) {
		return nil, fmt.Errorf("updater: bootstrap-cli sha256 mismatch for nyxveilctl")
	}
	if err := os.Chmod(tmpBin, 0o755); err != nil {
		return nil, err
	}

	install := opts.AtomicInstall
	if install == nil {
		install = atomicInstallCLI
	}
	if err := install(tmpBin, ctlPath); err != nil {
		return nil, fmt.Errorf("updater: bootstrap-cli install failed (old CLI left intact): %w", err)
	}

	return &BootstrapCLIResult{
		Version:     m.Version,
		Arch:        m.Arch,
		CtlPath:     ctlPath,
		PreviousSHA: prevSHA,
		NewSHA:      sum,
		Replaced:    true,
	}, nil
}

// atomicInstallCLI installs src over dest as root:root 0755 without touching other files.
// Uses temp+fsync+rename in the destination directory. On failure, dest is unchanged.
func atomicInstallCLI(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dest + ".bootstrap.tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	// CLI lives under /usr/local/sbin — intended owner root:root.
	_ = filemeta.Chown(tmp, 0, 0)
	// Keep a best-effort rollback copy without weakening the atomic install:
	// backup failure is non-fatal, and rename failure still leaves dest intact.
	if _, err := os.Stat(dest); err == nil {
		_ = copyFilePreserve(dest, dest+".prev")
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = filemeta.ApplyOwnerMode(dest, 0, 0, 0o755)
	return nil
}
