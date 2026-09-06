package configure

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/localconfig"
)

// Merge applies non-empty options onto a copy of base.
// Never clears NodeID / LocationID. ControlPlaneURL may change intentionally via --control-plane-url.
func Merge(base localconfig.File, opts Options) (localconfig.File, error) {
	out := base
	if out.NodeID == "" || out.ControlPlaneURL == "" {
		return out, fmt.Errorf("configure: existing node required (node_id and control_plane_url must be set)")
	}
	if u := strings.TrimSpace(opts.ControlPlaneURL); u != "" {
		normalized, err := NormalizeControlPlaneURL(u)
		if err != nil {
			return out, err
		}
		if !strings.EqualFold(strings.TrimRight(out.ControlPlaneURL, "/"), normalized) {
			out.ControlPlaneURL = normalized
			// Public CP cutover: clear SelfSignedPinned pin so daemon uses SystemTrust.
			out.ControlPlaneSPKIPin = ""
		}
	}
	if key := strings.TrimSpace(opts.PublicHost); key != "" {
		out.PublicHost = key
		if out.ServerName == "" || net.ParseIP(out.ServerName) != nil {
			out.ServerName = key
		}
	}
	if opts.DNSServers != "" {
		dns, err := ParseDNSServers(opts.DNSServers)
		if err != nil {
			return out, err
		}
		out.DNSServers = dns
	}
	if d := strings.TrimSpace(opts.TLSDomain); d != "" {
		out.ACMEDomain = d
		if e := strings.TrimSpace(opts.TLSEmail); e != "" {
			out.ACMEEmail = e
		}
	}
	if opts.TLSCert != "" && opts.TLSKey != "" {
		out.ACMEDomain = ""
		out.ACMEEmail = ""
	}
	if len(out.DNSServers) == 0 {
		return out, fmt.Errorf("configure: dns_servers must remain non-empty after merge")
	}
	if strings.TrimSpace(out.PublicHost) == "" {
		return out, fmt.Errorf("configure: public_host must remain non-empty after merge")
	}
	return out, nil
}

// AtomicSave writes cfg to path via temp + fsync + rename, preserving mode when possible.
func AtomicSave(path string, cfg *localconfig.File) error {
	if cfg == nil {
		return fmt.Errorf("configure: nil config")
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	var check localconfig.File
	if err := json.Unmarshal(b, &check); err != nil {
		return fmt.Errorf("configure: config JSON invalid after marshal: %w", err)
	}
	if check.NodeID != cfg.NodeID {
		return fmt.Errorf("configure: config identity fields changed unexpectedly")
	}
	if check.LocationID != cfg.LocationID {
		return fmt.Errorf("configure: location_id changed unexpectedly")
	}
	tmp := path + ".configure.tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
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
	// Preserve prior owner when replacing an existing config (root-run configure).
	uid, gid := -1, -1
	if prev, err := filemeta.CaptureMeta(path); err == nil && prev.Exists {
		uid, gid = prev.UID, prev.GID
	}
	if uid >= 0 && gid >= 0 {
		_ = filemeta.Chown(tmp, uid, gid)
	}
	_ = os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return filemeta.ApplyOwnerMode(path, uid, gid, mode)
}

// SnapshotFile copies path to destPath with ownership/mode metadata sidecar.
func SnapshotFile(src, dest string) error {
	dir := filepath.Dir(dest)
	name := filepath.Base(dest)
	_, err := filemeta.SnapshotFile(src, dir, name)
	return err
}

// RestoreFile restores snapshot content AND ownership/mode (never leave root:root TLS).
func RestoreFile(snap, dest string) error {
	dir := filepath.Dir(snap)
	name := filepath.Base(snap)
	s := &filemeta.Snapshot{
		Meta:     filemeta.Meta{Path: dest},
		DataFile: filepath.Join(dir, name+".data"),
		MetaFile: filepath.Join(dir, name+".meta.json"),
	}
	// Backward-compatible: old snapshots wrote raw bytes at dest path directly.
	if _, err := os.Stat(s.MetaFile); os.IsNotExist(err) {
		if _, err2 := os.Stat(snap); err2 == nil {
			b, rerr := os.ReadFile(snap)
			if rerr != nil {
				return rerr
			}
			meta, _ := filemeta.CaptureMeta(dest)
			uid, gid := meta.UID, meta.GID
			mode := meta.Mode
			if mode == 0 {
				mode = 0o644
			}
			if err := filemeta.AtomicWrite(dest, b, mode, uid, gid); err != nil {
				return err
			}
			// Prefer runtime TLS contract when restoring TLS material.
			base := strings.ToLower(filepath.Base(dest))
			if base == "tls.crt" || base == "tls.key" {
				_ = filemeta.EnforceRuntimeTLS(filepath.Dir(dest))
			}
			return nil
		}
		return nil
	}
	if metaRaw, err := os.ReadFile(s.MetaFile); err == nil {
		_ = json.Unmarshal(metaRaw, &s.Meta)
		s.Meta.Path = dest
	}
	if err := filemeta.Restore(s); err != nil {
		return err
	}
	base := strings.ToLower(filepath.Base(dest))
	if base == "tls.crt" || base == "tls.key" {
		_ = filemeta.EnforceRuntimeTLS(filepath.Dir(dest))
	}
	return nil
}
