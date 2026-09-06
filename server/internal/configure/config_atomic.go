package configure

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyxveil/server/internal/localconfig"
)

// Merge applies non-empty options onto a copy of base. Never clears NodeID / ControlPlaneURL / LocationID.
func Merge(base localconfig.File, opts Options) (localconfig.File, error) {
	out := base
	if out.NodeID == "" || out.ControlPlaneURL == "" {
		return out, fmt.Errorf("configure: existing node required (node_id and control_plane_url must be set)")
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
	if check.NodeID != cfg.NodeID || check.ControlPlaneURL != cfg.ControlPlaneURL {
		return fmt.Errorf("configure: config identity fields changed unexpectedly")
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
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// SnapshotFile copies path to destPath (best-effort).
func SnapshotFile(src, dest string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dest, b, 0o600)
}

// RestoreFile copies snapshot back if snapshot exists.
func RestoreFile(snap, dest string) error {
	b, err := os.ReadFile(snap)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(dest); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := dest + ".rollback.tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
