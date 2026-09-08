// Package updater downloads and atomically replaces nyxveil binaries.
//
// Manifest JSON (snake_case), multi-asset preferred:
//
//	{
//	  "version": "1.0.1",
//	  "arch": "linux/amd64",
//	  "min_core": "1.0.0",
//	  "min_protocol": 1,
//	  "assets": [
//	    {"name":"nyxveil-server","sha256":"...","url":"..."},
//	    {"name":"nyxveilctl","sha256":"...","url":"..."}
//	  ],
//	  "signature": "..."
//	}
//
// Backward compatible with single url/sha256 (applies to BinaryPath only).
package updater

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/version"
)

// UpdatePublicKey verifies release manifests (Ed25519).
// Server 1.1.4 trust-root rotation (prior private key unavailable on build host).
// Private key: GitHub Secret NYXVEIL_RELEASE_SIGNING_KEY / local .secrets/ only — never in git.
var UpdatePublicKey = ed25519.PublicKey{
	0xca, 0xf9, 0x21, 0x52, 0x1e, 0x21, 0x3c, 0xb1,
	0xbc, 0xdc, 0x2f, 0x9d, 0xf4, 0x81, 0x6c, 0x2e,
	0xcd, 0x43, 0x22, 0x2b, 0x23, 0xa4, 0x7d, 0x6f,
	0x86, 0x96, 0x72, 0xe6, 0xab, 0x0e, 0x79, 0xaf,
}

// Asset is one binary in a multi-asset release manifest.
type Asset struct {
	Name        string `json:"name"`
	SHA256      string `json:"sha256"`
	URL         string `json:"url"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	Required    bool   `json:"required"`
}

// Manifest describes a published node binary release.
type Manifest struct {
	Version     string  `json:"version"`
	Arch        string  `json:"arch"`
	SHA256      string  `json:"sha256,omitempty"` // legacy single-binary
	URL         string  `json:"url,omitempty"`    // legacy single-binary
	MinCore     string  `json:"min_core"`
	MinProtocol uint16  `json:"min_protocol"`
	Assets      []Asset `json:"assets,omitempty"`
	Signature   string  `json:"signature"`
}

// HealthCheck is invoked after replace; false triggers rollback.
type HealthCheck func() bool

// Updater performs download → verify → atomic replace → health/rollback.
type Updater struct {
	HTTP          *http.Client
	PublicKey     ed25519.PublicKey
	BinaryPath    string
	PrevPath      string
	MarkerPath    string
	ExtraBinaries map[string]string // asset name → install path
	ExtraPrev     map[string]string // asset name → previous backup path
	LocalDir      string            // optional verified offline release directory

	// StateDir holds TLS + rollback marker; default paths.StateDir.
	StateDir string
	// EnforceOwnership after commit/rollback (overridable in tests).
	EnforceOwnership func(stateDir string) error
	// DaemonReload runs after installing or rolling back systemd unit assets.
	// When nil on Linux, systemctl daemon-reload is used; tests may stub this.
	DaemonReload func() error
}

// New returns an updater with default HTTP client and embedded public key.
func New(binaryPath, prevPath, markerPath string) *Updater {
	return &Updater{
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		PublicKey:  UpdatePublicKey,
		BinaryPath: binaryPath,
		PrevPath:   prevPath,
		MarkerPath: markerPath,
	}
}

// ParseManifest unmarshals and verifies signature + SHA field format.
func ParseManifest(data []byte, pub ed25519.PublicKey) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version == "" {
		return nil, fmt.Errorf("updater: manifest missing version")
	}
	if len(m.Assets) == 0 && (m.SHA256 == "" || m.URL == "") {
		return nil, fmt.Errorf("updater: manifest missing required fields")
	}
	for _, a := range m.Assets {
		if a.Name == "" || a.SHA256 == "" || a.URL == "" {
			return nil, fmt.Errorf("updater: asset missing fields")
		}
		if _, err := hex.DecodeString(strings.TrimSpace(a.SHA256)); err != nil {
			return nil, fmt.Errorf("updater: bad asset sha256: %w", err)
		}
	}
	if pub == nil {
		pub = UpdatePublicKey
	}
	if isZeroKey(pub) {
		return nil, fmt.Errorf("updater: update public key is placeholder; refusing")
	}
	sig, err := base64.RawURLEncoding.DecodeString(m.Signature)
	if err != nil {
		sig, err = base64.StdEncoding.DecodeString(m.Signature)
		if err != nil {
			return nil, fmt.Errorf("updater: bad signature encoding: %w", err)
		}
	}
	msg := CanonicalManifestBytes(&m)
	if !ed25519.Verify(pub, msg, sig) {
		return nil, fmt.Errorf("updater: manifest signature invalid")
	}
	if m.SHA256 != "" {
		if _, err := hex.DecodeString(strings.TrimSpace(m.SHA256)); err != nil {
			return nil, fmt.Errorf("updater: bad sha256 hex: %w", err)
		}
	}
	return &m, nil
}

// CanonicalManifestBytes builds the signed payload (no signature field).
func CanonicalManifestBytes(m *Manifest) []byte {
	type legacySignedAsset struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		URL    string `json:"url"`
	}
	type signedAsset struct {
		Name        string `json:"name"`
		SHA256      string `json:"sha256"`
		URL         string `json:"url"`
		Destination string `json:"destination"`
		Mode        string `json:"mode"`
		Required    bool   `json:"required"`
	}
	type signed struct {
		Version     string        `json:"version"`
		Arch        string        `json:"arch"`
		SHA256      string        `json:"sha256,omitempty"`
		URL         string        `json:"url,omitempty"`
		MinCore     string        `json:"min_core"`
		MinProtocol uint16        `json:"min_protocol"`
		Assets      []signedAsset `json:"assets,omitempty"`
	}
	legacyAssets := true
	for _, a := range m.Assets {
		if a.Destination != "" || a.Mode != "" || a.Required {
			legacyAssets = false
			break
		}
	}
	if legacyAssets && len(m.Assets) > 0 {
		type legacySigned struct {
			Version     string              `json:"version"`
			Arch        string              `json:"arch"`
			SHA256      string              `json:"sha256,omitempty"`
			URL         string              `json:"url,omitempty"`
			MinCore     string              `json:"min_core"`
			MinProtocol uint16              `json:"min_protocol"`
			Assets      []legacySignedAsset `json:"assets,omitempty"`
		}
		s := legacySigned{
			Version: m.Version, Arch: m.Arch, SHA256: m.SHA256, URL: m.URL,
			MinCore: m.MinCore, MinProtocol: m.MinProtocol,
		}
		for _, a := range m.Assets {
			s.Assets = append(s.Assets, legacySignedAsset{Name: a.Name, SHA256: a.SHA256, URL: a.URL})
		}
		b, _ := json.Marshal(s)
		return b
	}
	s := signed{
		Version:     m.Version,
		Arch:        m.Arch,
		SHA256:      m.SHA256,
		URL:         m.URL,
		MinCore:     m.MinCore,
		MinProtocol: m.MinProtocol,
	}
	for _, a := range m.Assets {
		s.Assets = append(s.Assets, signedAsset{
			Name: a.Name, SHA256: a.SHA256, URL: a.URL, Destination: a.Destination,
			Mode: a.Mode, Required: a.Required,
		})
	}
	b, _ := json.Marshal(s)
	return b
}

// ArchString returns GOOS/GOARCH for manifest matching.
func ArchString() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

type replaceJob struct {
	name, url, sha, dest, prev string
	mode                       os.FileMode
	existed                    bool
}

// IncompleteInstallError reports a required release asset that is missing,
// has the wrong contents, or has the wrong mode at its committed destination.
type IncompleteInstallError struct {
	Cause error
}

func (e *IncompleteInstallError) Error() string {
	return "updater: incomplete release install: " + e.Cause.Error()
}

func (e *IncompleteInstallError) Unwrap() error { return e.Cause }

// RequiredAssetNames must appear in every signed multi-asset release manifest.
var RequiredAssetNames = []string{
	"nyxveil-server",
	"nyxveilctl",
	"nyxveil-catalog-verify",
	"production-gate",
	"share-version",
	"share-third-party-core",
	"nyxveil-update-service",
	"nyxveil-management-polkit",
}

type assetContract struct {
	destination string
	mode        os.FileMode
}

var productionAssets = map[string]assetContract{
	"nyxveil-server":            {paths.BinaryPath(), 0o755},
	"nyxveilctl":                {paths.BinDir + "/nyxveilctl", 0o755},
	"nyxveil-catalog-verify":    {paths.CatalogVerify(), 0o755},
	"production-gate":           {paths.ProductionGate(), 0o755},
	"share-version":             {paths.ShareVersion(), 0o644},
	"share-third-party-core":    {paths.ShareThirdParty(), 0o644},
	"nyxveil-update-service":    {paths.UpdateServiceUnit(), 0o644},
	"nyxveil-management-polkit": {paths.ManagementPolkitRule(), 0o644},
}

func requiresDaemonReload(name string) bool {
	return name == "nyxveil-update-service"
}

func jobsNeedDaemonReload(jobs []replaceJob) bool {
	for _, j := range jobs {
		if requiresDaemonReload(j.name) {
			return true
		}
	}
	return false
}

func assetMode(name string) os.FileMode {
	if contract, ok := productionAssets[name]; ok {
		return contract.mode
	}
	return 0o755
}

// ParseMode parses a manifest's four-digit octal permission mode.
func ParseMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, fmt.Errorf("updater: invalid asset mode %q", value)
	}
	n, err := strconv.ParseUint(value, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("updater: invalid asset mode %q", value)
	}
	return os.FileMode(n), nil
}

// Apply downloads assets, verifies SHA-256, replaces atomically, runs health, rolls back on failure.
func (u *Updater) Apply(m *Manifest, health HealthCheck) error {
	if m == nil {
		return fmt.Errorf("updater: nil manifest")
	}
	wantArch := ArchString()
	if m.Arch != "" && m.Arch != wantArch {
		return fmt.Errorf("updater: arch mismatch have %s want %s", wantArch, m.Arch)
	}
	if err := CheckCompatibility(m, version.CoreVersion, version.ProtocolNumber); err != nil {
		return err
	}

	jobs, err := u.planJobs(m)
	if err != nil {
		return err
	}

	stateDir := u.StateDir
	if stateDir == "" {
		stateDir = paths.StateDir
	}
	tlsCert := filepath.Join(stateDir, "tls.crt")
	tlsKey := filepath.Join(stateDir, "tls.key")

	tmpDir, err := os.MkdirTemp("", "nyxveil-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	snapDir := filepath.Join(tmpDir, "tls-meta-snap")
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		return err
	}
	var snaps []*filemeta.Snapshot
	for _, name := range []string{"tls.crt", "tls.key"} {
		src := filepath.Join(stateDir, name)
		s, err := filemeta.SnapshotFile(src, snapDir, name)
		if err != nil {
			return fmt.Errorf("updater: snapshot %s: %w", name, err)
		}
		snaps = append(snaps, s)
	}

	type prepared struct {
		replaceJob
		tmp string
	}
	var preparedList []prepared
	for _, j := range jobs {
		tmpBin := filepath.Join(tmpDir, j.name+".new")
		if err := u.download(j.url, tmpBin); err != nil {
			return err
		}
		sum, err := fileSHA256(tmpBin)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, strings.TrimSpace(j.sha)) {
			return fmt.Errorf("updater: sha256 mismatch for %s", j.name)
		}
		mode := j.mode
		if mode == 0 {
			mode = assetMode(j.name)
		}
		if err := os.Chmod(tmpBin, mode); err != nil {
			return err
		}
		preparedList = append(preparedList, prepared{replaceJob: j, tmp: tmpBin})
	}

	if u.MarkerPath != "" {
		_ = os.WriteFile(u.MarkerPath, []byte(m.Version), 0o644)
	}

	restoreTLS := func() error {
		var first error
		for _, s := range snaps {
			if s == nil {
				continue
			}
			// Ensure restore targets live TLS paths.
			if filepath.Base(s.Meta.Path) == "tls.crt" {
				s.Meta.Path = tlsCert
			}
			if filepath.Base(s.Meta.Path) == "tls.key" {
				s.Meta.Path = tlsKey
			}
			if err := filemeta.Restore(s); err != nil && first == nil {
				first = err
			}
		}
		if err := u.enforceOwnership(stateDir); err != nil && first == nil {
			first = err
		}
		return first
	}

	replaced := make([]replaceJob, 0, len(preparedList))
	for _, p := range preparedList {
		if p.dest == "" {
			return fmt.Errorf("updater: empty binary path for %s", p.name)
		}
		if err := os.MkdirAll(filepath.Dir(p.dest), 0o755); err != nil {
			return err
		}
		job := p.replaceJob
		if _, err := os.Stat(p.dest); err == nil {
			job.existed = true
		}
		mode := job.mode
		if mode == 0 {
			mode = assetMode(job.name)
		}
		job.mode = mode
		if p.prev != "" {
			if err := os.MkdirAll(filepath.Dir(p.prev), 0o755); err != nil {
				return err
			}
			_ = os.Remove(p.prev)
			if job.existed {
				if err := copyFilePreserve(p.dest, p.prev); err != nil {
					_ = u.rollbackJobs(replaced)
					_ = restoreTLS()
					return fmt.Errorf("updater: backup %s: %w", p.name, err)
				}
			}
		}
		if err := atomicReplaceMode(p.tmp, p.dest, mode); err != nil {
			_ = u.rollbackJobs(replaced)
			_ = restoreTLS()
			return err
		}
		replaced = append(replaced, job)
	}

	if err := VerifyCommitted(replaced); err != nil {
		binErr := u.rollbackJobs(replaced)
		tlsErr := restoreTLS()
		if binErr != nil {
			return fmt.Errorf("updater: committed file verification failed: %w; rollback failed: %v (tls restore err: %v)", err, binErr, tlsErr)
		}
		if tlsErr != nil {
			return fmt.Errorf("updater: committed file verification failed: %w; binaries rolled back but TLS metadata restore failed: %v", err, tlsErr)
		}
		return fmt.Errorf("updater: committed file verification failed: %w; rolled back", err)
	}

	if jobsNeedDaemonReload(replaced) {
		if err := u.runDaemonReload(); err != nil {
			binErr := u.rollbackJobs(replaced)
			tlsErr := restoreTLS()
			if binErr != nil {
				return fmt.Errorf("updater: daemon-reload failed: %w; rollback failed: %v (tls restore err: %v)", err, binErr, tlsErr)
			}
			if tlsErr != nil {
				return fmt.Errorf("updater: daemon-reload failed: %w; binaries rolled back but TLS metadata restore failed: %v", err, tlsErr)
			}
			return fmt.Errorf("updater: daemon-reload failed: %w; rolled back", err)
		}
	}

	if health != nil && !health() {
		binErr := u.rollbackJobs(replaced)
		tlsErr := restoreTLS()
		if binErr != nil {
			return fmt.Errorf("updater: health failed and binary rollback failed: %w (tls restore err: %v)", binErr, tlsErr)
		}
		if tlsErr != nil {
			return fmt.Errorf("updater: health check failed; binaries rolled back but TLS metadata restore failed: %w", tlsErr)
		}
		return fmt.Errorf("updater: health check failed; rolled back")
	}
	// Successful path: ensure runtime TLS is readable by service user even if a
	// prior root-owned rewrite left bad ownership on disk.
	_ = u.enforceOwnership(stateDir)
	if u.MarkerPath != "" {
		_ = os.Remove(u.MarkerPath)
	}
	return nil
}

func (u *Updater) enforceOwnership(stateDir string) error {
	fn := u.EnforceOwnership
	if fn == nil {
		fn = filemeta.EnforceRuntimeTLS
	}
	return fn(stateDir)
}

func (u *Updater) planJobs(m *Manifest) ([]replaceJob, error) {
	var jobs []replaceJob
	if len(m.Assets) > 0 {
		assets := make(map[string]Asset, len(m.Assets))
		for _, a := range m.Assets {
			if _, duplicate := assets[a.Name]; duplicate {
				return nil, fmt.Errorf("updater: duplicate asset %q", a.Name)
			}
			if _, known := productionAssets[a.Name]; a.Required && !known {
				return nil, fmt.Errorf("updater: required asset %q is not in the install allowlist", a.Name)
			}
			assets[a.Name] = a
		}
		for _, name := range RequiredAssetNames {
			if _, ok := assets[name]; !ok {
				return nil, fmt.Errorf("updater: required asset %q missing from signed manifest", name)
			}
			if name != "nyxveil-server" && u.ExtraBinaries != nil && u.ExtraBinaries[name] == "" {
				return nil, fmt.Errorf("updater: required asset %q has no ExtraBinaries destination", name)
			}
		}

		for _, name := range RequiredAssetNames {
			a := assets[name]
			contract := productionAssets[name]
			if a.Destination != "" && a.Destination != contract.destination {
				return nil, fmt.Errorf("updater: asset %q destination %q does not match allowlist %q", name, a.Destination, contract.destination)
			}
			dest := contract.destination
			extraOverride := u.ExtraBinaries != nil && u.ExtraBinaries[name] != ""
			if extraOverride {
				dest = u.ExtraBinaries[name]
			} else if a.Destination != "" {
				dest = a.Destination
			}
			if name == "nyxveil-server" && !extraOverride && u.BinaryPath != "" {
				dest = u.BinaryPath
			}
			if dest == "" {
				return nil, fmt.Errorf("updater: required asset %q has no destination", name)
			}
			mode := contract.mode
			if a.Mode != "" {
				parsed, err := ParseMode(a.Mode)
				if err != nil {
					return nil, fmt.Errorf("updater: asset %q: %w", name, err)
				}
				if parsed != contract.mode {
					return nil, fmt.Errorf("updater: asset %q mode %04o does not match allowlist %04o", name, parsed, contract.mode)
				}
				mode = parsed
			}
			prev := ""
			if name == "nyxveil-server" {
				prev = u.PrevPath
			} else if u.ExtraPrev != nil {
				prev = u.ExtraPrev[name]
			}
			if prev == "" && extraOverride {
				prev = dest + ".prev"
			}
			jobs = append(jobs, replaceJob{
				name: name, url: a.URL, sha: a.SHA256, dest: dest, prev: prev, mode: mode,
			})
		}
		return jobs, nil
	}
	return []replaceJob{{name: "nyxveil-server", url: m.URL, sha: m.SHA256, dest: u.BinaryPath, prev: u.PrevPath, mode: 0o755}}, nil
}

// VerifyReleaseInstall checks that every required manifest asset is present at
// the destinations configured on u with the signed hash and required mode.
func (u *Updater) VerifyReleaseInstall(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("updater: nil manifest")
	}
	jobs, err := u.planJobs(m)
	if err != nil {
		return err
	}
	if err := VerifyCommitted(jobs); err != nil {
		return &IncompleteInstallError{Cause: err}
	}
	return nil
}

// VerifyCommitted verifies every file after atomic replacement and before health.
func VerifyCommitted(jobs []replaceJob) error {
	for _, j := range jobs {
		st, err := os.Stat(j.dest)
		if err != nil {
			return fmt.Errorf("%s missing at %s: %w", j.name, j.dest, err)
		}
		wantMode := j.mode.Perm()
		gotMode := st.Mode().Perm()
		// Windows does not preserve Unix permission bits; executable-mode
		// enforcement is meaningful only on Unix release hosts.
		if runtime.GOOS != "windows" && gotMode != wantMode {
			return fmt.Errorf("%s mode at %s is %04o, want %04o", j.name, j.dest, st.Mode().Perm(), j.mode.Perm())
		}
		sum, err := fileSHA256(j.dest)
		if err != nil {
			return fmt.Errorf("%s hash at %s: %w", j.name, j.dest, err)
		}
		if !strings.EqualFold(sum, strings.TrimSpace(j.sha)) {
			return fmt.Errorf("%s sha256 at %s is %s, want %s", j.name, j.dest, sum, j.sha)
		}
	}
	return nil
}

func (u *Updater) rollbackJobs(jobs []replaceJob) error {
	var first error
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if !j.existed {
			if err := os.Remove(j.dest); err != nil && !os.IsNotExist(err) && first == nil {
				first = err
			}
			continue
		}
		if j.prev == "" {
			continue
		}
		if _, err := os.Stat(j.prev); err != nil {
			continue
		}
		mode := j.mode
		if mode == 0 {
			mode = assetMode(j.name)
		}
		if err := atomicReplaceMode(j.prev, j.dest, mode); err != nil && first == nil {
			first = err
		}
	}
	if jobsNeedDaemonReload(jobs) {
		if err := u.runDaemonReload(); err != nil && first == nil {
			first = err
		}
	}
	if u.MarkerPath != "" {
		_ = os.Remove(u.MarkerPath)
	}
	return first
}

func (u *Updater) runDaemonReload() error {
	if u.DaemonReload != nil {
		return u.DaemonReload()
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	return systemdDaemonReload()
}

func systemdDaemonReload() error {
	cmd := exec.Command("systemctl", "daemon-reload")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Rollback restores PrevPath over BinaryPath (and extras).
func (u *Updater) Rollback() error {
	jobs := []replaceJob{{name: "nyxveil-server", dest: u.BinaryPath, prev: u.PrevPath, existed: true, mode: 0o755}}
	for name, prev := range u.ExtraPrev {
		dest := ""
		if u.ExtraBinaries != nil {
			dest = u.ExtraBinaries[name]
		}
		if dest == "" {
			continue
		}
		jobs = append(jobs, replaceJob{name: name, dest: dest, prev: prev, existed: true, mode: assetMode(name)})
	}
	if u.PrevPath == "" {
		return fmt.Errorf("updater: no previous binary path")
	}
	if _, err := os.Stat(u.PrevPath); err != nil {
		return fmt.Errorf("updater: previous binary missing: %w", err)
	}
	return u.rollbackJobs(jobs)
}

// RollbackInstalled restores every backed-up asset from *.prev after a failed
// post-check that already committed new files (self-update handoff failure).
func (u *Updater) RollbackInstalled() error {
	return u.Rollback()
}

func (u *Updater) download(url, dest string) error {
	if u.LocalDir != "" {
		name := filepath.Base(strings.TrimSpace(url))
		if name == "." || name == string(filepath.Separator) || name == "" {
			return fmt.Errorf("updater: invalid local asset URL %q", url)
		}
		src := filepath.Join(u.LocalDir, name)
		if err := copyFilePreserve(src, dest); err != nil {
			return fmt.Errorf("updater: read local asset %s: %w", name, err)
		}
		return nil
	}
	resp, err := u.HTTP.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("updater: download status %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, io.LimitReader(resp.Body, 256<<20))
	return err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func atomicReplace(src, dest string) error {
	return atomicReplaceMode(src, dest, 0o755)
}

func atomicReplaceMode(src, dest string, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o755
	}
	dir := filepath.Dir(dest)
	tmp := filepath.Join(dir, ".nyxveil-replace-"+filepath.Base(dest))
	if err := copyFilePreserve(src, tmp); err != nil {
		return err
	}
	uid, gid := -1, -1
	if prev, err := filemeta.CaptureMeta(dest); err == nil && prev.Exists {
		uid, gid = prev.UID, prev.GID
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if uid >= 0 && gid >= 0 {
		_ = filemeta.Chown(tmp, uid, gid)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return filemeta.ApplyOwnerMode(dest, uid, gid, mode)
}

func copyFile(src, dest string) error {
	return copyFilePreserve(src, dest)
}

func copyFilePreserve(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o755)
	uid, gid := -1, -1
	if st, err := in.Stat(); err == nil {
		mode = st.Mode().Perm()
		if m, err := filemeta.CaptureMeta(src); err == nil {
			uid, gid = m.UID, m.GID
		}
	}
	b, err := io.ReadAll(io.LimitReader(in, 256<<20))
	if err != nil {
		return err
	}
	return filemeta.AtomicWrite(dest, b, mode, uid, gid)
}

func isZeroKey(pub ed25519.PublicKey) bool {
	for _, b := range pub {
		if b != 0 {
			return false
		}
	}
	return true
}

// SignManifest is a test/helper that signs CanonicalManifestBytes with priv.
func SignManifest(m *Manifest, priv ed25519.PrivateKey) {
	m.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, CanonicalManifestBytes(m)))
}

// DefaultReleaseBase is used when server.json has no update_url.
const DefaultReleaseBase = "https://github.com/Moroz1212/Nyxveil/releases/latest/download"

// DefaultManifestURL returns the architecture-aware release manifest URL.
func DefaultManifestURL() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		arch = "amd64"
	}
	return DefaultReleaseBase + "/release-manifest-linux-" + arch + ".json"
}

// CheckCompatibility verifies min_core / min_protocol against the running node.
func CheckCompatibility(m *Manifest, coreVersion string, protocol uint16) error {
	if m == nil {
		return fmt.Errorf("updater: nil manifest")
	}
	if m.MinProtocol > 0 && protocol < m.MinProtocol {
		return fmt.Errorf("updater: protocol %d < required min_protocol %d", protocol, m.MinProtocol)
	}
	if strings.TrimSpace(m.MinCore) != "" {
		cmp, err := compareSemver(coreVersion, m.MinCore)
		if err != nil {
			return fmt.Errorf("updater: min_core compare: %w", err)
		}
		if cmp < 0 {
			return fmt.Errorf("updater: core %s < required min_core %s", coreVersion, m.MinCore)
		}
	}
	return nil
}

// compareSemver returns -1 if a<b, 0 if equal, 1 if a>b (major.minor.patch, missing=0).
func compareSemver(a, b string) (int, error) {
	pa, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1, nil
		}
		if pa[i] > pb[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseSemver(s string) ([3]int, error) {
	s = strings.TrimSpace(strings.TrimPrefix(s, "v"))
	parts := strings.Split(s, ".")
	var out [3]int
	if len(parts) == 0 || parts[0] == "" {
		return out, fmt.Errorf("empty version")
	}
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return out, fmt.Errorf("bad version %q", s)
		}
		out[i] = n
	}
	return out, nil
}
