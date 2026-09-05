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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/version"
)

// UpdatePublicKey verifies release manifests (Ed25519).
// Candidate key for Nyxveil Server 1.0.0 CLEAN-HOST builds.
// Private key: GitHub Secret NYXVEIL_RELEASE_SIGNING_KEY / local .secrets/ only — never in git.
var UpdatePublicKey = ed25519.PublicKey{
	0xf6, 0x3d, 0x2c, 0x80, 0x01, 0xdf, 0x3d, 0x7b,
	0x2e, 0xfd, 0xd1, 0x71, 0xa1, 0x64, 0x63, 0x26,
	0x0c, 0xb7, 0x19, 0x0d, 0x61, 0xef, 0x56, 0x44,
	0x19, 0xcc, 0x08, 0x36, 0x77, 0x7d, 0x17, 0x6f,
}

// Asset is one binary in a multi-asset release manifest.
type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url"`
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
	type signedAsset struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		URL    string `json:"url"`
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
	s := signed{
		Version:     m.Version,
		Arch:        m.Arch,
		SHA256:      m.SHA256,
		URL:         m.URL,
		MinCore:     m.MinCore,
		MinProtocol: m.MinProtocol,
	}
	for _, a := range m.Assets {
		s.Assets = append(s.Assets, signedAsset{Name: a.Name, SHA256: a.SHA256, URL: a.URL})
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

	tmpDir, err := os.MkdirTemp("", "nyxveil-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

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
		if err := os.Chmod(tmpBin, 0o755); err != nil {
			return err
		}
		preparedList = append(preparedList, prepared{replaceJob: j, tmp: tmpBin})
	}

	if u.MarkerPath != "" {
		_ = os.WriteFile(u.MarkerPath, []byte(m.Version), 0o644)
	}

	replaced := make([]replaceJob, 0, len(preparedList))
	for _, p := range preparedList {
		if p.dest == "" {
			return fmt.Errorf("updater: empty binary path for %s", p.name)
		}
		if err := os.MkdirAll(filepath.Dir(p.dest), 0o755); err != nil {
			return err
		}
		if p.prev != "" {
			if err := os.MkdirAll(filepath.Dir(p.prev), 0o755); err != nil {
				return err
			}
			if _, err := os.Stat(p.dest); err == nil {
				_ = os.Remove(p.prev)
				if err := copyFile(p.dest, p.prev); err != nil {
					_ = u.rollbackJobs(replaced)
					return fmt.Errorf("updater: backup %s: %w", p.name, err)
				}
			}
		}
		if err := atomicReplace(p.tmp, p.dest); err != nil {
			_ = u.rollbackJobs(replaced)
			return err
		}
		replaced = append(replaced, p.replaceJob)
	}

	if health != nil && !health() {
		if err := u.rollbackJobs(replaced); err != nil {
			return fmt.Errorf("updater: health failed and rollback failed: %w", err)
		}
		return fmt.Errorf("updater: health check failed; rolled back")
	}
	if u.MarkerPath != "" {
		_ = os.Remove(u.MarkerPath)
	}
	return nil
}

func (u *Updater) planJobs(m *Manifest) ([]replaceJob, error) {
	var jobs []replaceJob
	if len(m.Assets) > 0 {
		for _, a := range m.Assets {
			dest, prev := "", ""
			switch a.Name {
			case "nyxveil-server", "server":
				dest, prev = u.BinaryPath, u.PrevPath
			default:
				if u.ExtraBinaries != nil {
					dest = u.ExtraBinaries[a.Name]
				}
				if u.ExtraPrev != nil {
					prev = u.ExtraPrev[a.Name]
				}
			}
			if dest == "" {
				continue
			}
			jobs = append(jobs, replaceJob{name: a.Name, url: a.URL, sha: a.SHA256, dest: dest, prev: prev})
		}
		if len(jobs) == 0 {
			return nil, fmt.Errorf("updater: no applicable assets")
		}
		return jobs, nil
	}
	return []replaceJob{{name: "nyxveil-server", url: m.URL, sha: m.SHA256, dest: u.BinaryPath, prev: u.PrevPath}}, nil
}

func (u *Updater) rollbackJobs(jobs []replaceJob) error {
	var first error
	for i := len(jobs) - 1; i >= 0; i-- {
		j := jobs[i]
		if j.prev == "" {
			continue
		}
		if _, err := os.Stat(j.prev); err != nil {
			continue
		}
		if err := atomicReplace(j.prev, j.dest); err != nil && first == nil {
			first = err
		}
	}
	if u.MarkerPath != "" {
		_ = os.Remove(u.MarkerPath)
	}
	return first
}

// Rollback restores PrevPath over BinaryPath (and extras).
func (u *Updater) Rollback() error {
	jobs := []replaceJob{{name: "nyxveil-server", dest: u.BinaryPath, prev: u.PrevPath}}
	for name, prev := range u.ExtraPrev {
		dest := ""
		if u.ExtraBinaries != nil {
			dest = u.ExtraBinaries[name]
		}
		if dest == "" {
			continue
		}
		jobs = append(jobs, replaceJob{name: name, dest: dest, prev: prev})
	}
	if u.PrevPath == "" {
		return fmt.Errorf("updater: no previous binary path")
	}
	if _, err := os.Stat(u.PrevPath); err != nil {
		return fmt.Errorf("updater: previous binary missing: %w", err)
	}
	return u.rollbackJobs(jobs)
}

func (u *Updater) download(url, dest string) error {
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
	dir := filepath.Dir(dest)
	tmp := filepath.Join(dir, ".nyxveil-replace-"+filepath.Base(dest))
	if err := copyFile(src, tmp); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
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
