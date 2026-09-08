package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nyxveil/server/internal/paths"
	"github.com/nyxveil/server/internal/updater"
	"github.com/nyxveil/server/internal/version"
)

const (
	nyxveilUpdateUnit = "nyxveil-update.service"
	updateMarkerName  = "update-command.json"
)

var serverReleaseTag = regexp.MustCompile(`(?i)^server-v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$`)

type updateMarker struct {
	CommandID       string `json:"command_id"`
	PreviousVersion string `json:"previous_version"`
	TargetVersion   string `json:"target_version"`
	Phase           string `json:"phase"`
	StartedAt       string `json:"started_at"`
}

func (n *Node) executeUpdateNodeLatest(ctx context.Context, commandID string) {
	prev := version.ServerVersion
	log.Printf("runtime: update %s phase=CheckingLatest", commandID)

	latest, err := resolveLatestStableServerVersion(ctx)
	if err != nil {
		n.reportCommandFailure(ctx, commandID, "latest_failed", err.Error())
		return
	}

	cmp, err := compareSemVer(prev, latest)
	if err != nil {
		n.reportCommandFailure(ctx, commandID, "version_parse", err.Error())
		return
	}
	if cmp == 0 {
		n.reportCommandSuccess(ctx, commandID, "already_current", "Already up to date: "+prev)
		return
	}
	if cmp > 0 {
		n.reportCommandSuccess(ctx, commandID, "ahead", "Node version is newer than latest published release")
		return
	}

	n.writeUpdateMarker(updateMarker{
		CommandID:       commandID,
		PreviousVersion: prev,
		TargetVersion:   latest,
		Phase:           "Downloading",
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	})

	if err := startUpdateUnit(); err == nil {
		log.Printf("runtime: update %s started %s", commandID, nyxveilUpdateUnit)
		return
	}

	log.Printf("runtime: update %s phase=Verifying (in-process fallback)", commandID)
	manifestURL := updater.ManifestURLForVersion(latest)
	data, err := downloadBytes(ctx, manifestURL)
	if err != nil {
		n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "manifest_download", err.Error())
		return
	}
	mani, err := updater.ParseManifest(data)
	if err != nil {
		n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "manifest_verify", err.Error())
		return
	}
	if strings.TrimPrefix(strings.TrimSpace(mani.Version), "v") != strings.TrimPrefix(latest, "v") {
		n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "version_mismatch", "manifest version does not match latest")
		return
	}

	log.Printf("runtime: update %s phase=Installing", commandID)
	binPath, err := os.Executable()
	if err != nil {
		binPath = os.Args[0]
	}
	u := &updater.Updater{
		HTTP:       &http.Client{Timeout: 5 * time.Minute},
		BinaryPath: binPath,
		StateDir:   filepath.Dir(paths.CommandsState()),
	}
	if err := u.Apply(mani, nil); err != nil {
		n.clearUpdateMarker()
		n.reportCommandFailure(ctx, commandID, "apply_failed", err.Error())
		return
	}

	n.writeUpdateMarker(updateMarker{
		CommandID:       commandID,
		PreviousVersion: prev,
		TargetVersion:   latest,
		Phase:           "Restarting",
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	})
	_ = systemdRestartService(nyxveilServiceUnit)
}

func (n *Node) completePendingUpdate(ctx context.Context) {
	m, ok := n.readUpdateMarker()
	if !ok || strings.TrimSpace(m.CommandID) == "" {
		return
	}
	cur := version.ServerVersion
	target := strings.TrimPrefix(strings.TrimSpace(m.TargetVersion), "v")
	got := strings.TrimPrefix(strings.TrimSpace(cur), "v")
	if got == target {
		n.reportCommandSuccess(ctx, m.CommandID, "updated", "Runtime version confirmed: "+cur)
		n.clearUpdateMarker()
		return
	}
	n.reportCommandFailure(ctx, m.CommandID, "version_not_confirmed",
		fmt.Sprintf("expected %s got %s", m.TargetVersion, cur))
	n.clearUpdateMarker()
}

func startUpdateUnit() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("update unit unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command("systemctl", "start", nyxveilUpdateUnit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveLatestStableServerVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/Moroz1212/Nyxveil/releases?per_page=40", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nyxveil-server")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github releases: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	var releases []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", err
	}
	var best string
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		m := serverReleaseTag.FindStringSubmatch(strings.TrimSpace(r.TagName))
		if len(m) != 2 {
			continue
		}
		ver := m[1]
		if strings.Contains(ver, "-") {
			continue
		}
		if best == "" {
			best = ver
			continue
		}
		cmp, err := compareSemVer(ver, best)
		if err == nil && cmp > 0 {
			best = ver
		}
	}
	if best == "" {
		return "", fmt.Errorf("no stable server-v release found")
	}
	return best, nil
}

func compareSemVer(a, b string) (int, error) {
	pa, err := parseSemVerParts(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseSemVerParts(b)
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

func parseSemVerParts(v string) ([3]int, error) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid semver %q", v)
	}
	for i := 0; i < 3; i++ {
		var n int
		if _, err := fmt.Sscanf(parts[i], "%d", &n); err != nil {
			return out, err
		}
		out[i] = n
	}
	return out, nil
}

func downloadBytes(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (n *Node) updateMarkerPath() string {
	base := filepath.Dir(paths.CommandsState())
	if n.opts.KeyPath != "" {
		base = filepath.Dir(n.opts.KeyPath)
	}
	return filepath.Join(base, "management", updateMarkerName)
}

func (n *Node) writeUpdateMarker(m updateMarker) {
	path := n.updateMarkerPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(path, raw, 0o600)
}

func (n *Node) readUpdateMarker() (updateMarker, bool) {
	raw, err := os.ReadFile(n.updateMarkerPath())
	if err != nil {
		return updateMarker{}, false
	}
	var m updateMarker
	if json.Unmarshal(raw, &m) != nil {
		return updateMarker{}, false
	}
	return m, true
}

func (n *Node) clearUpdateMarker() {
	_ = os.Remove(n.updateMarkerPath())
}
