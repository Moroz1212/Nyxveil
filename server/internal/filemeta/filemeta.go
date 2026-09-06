// Package filemeta snapshots and restores file contents with ownership/mode metadata.
//
// Root cause this addresses: os.WriteFile / rename as root recreates inodes as root:root,
// which breaks User=nyxveil services that must read /var/lib/nyxveil/tls.key (0600).
package filemeta

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	// RuntimeTLSCertMode is the production mode for tls.crt.
	RuntimeTLSCertMode = 0o644
	// RuntimeTLSKeyMode is the production mode for tls.key (never world-readable).
	RuntimeTLSKeyMode = 0o600
	// RuntimeStateDirMode is /var/lib/nyxveil.
	RuntimeStateDirMode = 0o700

	ServiceUser  = "nyxveil"
	ServiceGroup = "nyxveil"
)

// Meta is portable file metadata (no private key material).
type Meta struct {
	Path       string      `json:"path"`
	Exists     bool        `json:"exists"`
	Mode       os.FileMode `json:"mode"`
	UID        int         `json:"uid"` // -1 unknown / not applicable
	GID        int         `json:"gid"`
	IsSymlink  bool        `json:"is_symlink,omitempty"`
	LinkTarget string      `json:"link_target,omitempty"`
}

// Snapshot holds on-disk content backup + metadata sidecar.
type Snapshot struct {
	Meta     Meta   `json:"meta"`
	DataFile string `json:"data_file,omitempty"` // empty when !Exists
	MetaFile string `json:"meta_file"`
}

// LookupServiceIDs resolves the nyxveil service account (overridable in tests).
var LookupServiceIDs = func() (uid, gid int, err error) {
	ids, err := lookupServiceIDs()
	if err != nil {
		return -1, -1, err
	}
	return ids.UID, ids.GID, nil
}

// Hooks (overridable in tests).
var (
	Chown    = chownPath
	Lstat    = os.Lstat
	ReadLink = os.Readlink
)

type serviceIDs struct{ UID, GID int }

func lookupServiceIDs() (serviceIDs, error) {
	u, err := user.Lookup(ServiceUser)
	if err != nil {
		return serviceIDs{-1, -1}, err
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return serviceIDs{-1, -1}, fmt.Errorf("filemeta: parse nyxveil uid/gid: %v %v", err1, err2)
	}
	return serviceIDs{UID: uid, GID: gid}, nil
}

// CaptureMeta reads ownership/mode/symlink state without reading file contents.
func CaptureMeta(path string) (Meta, error) {
	m := Meta{Path: path, UID: -1, GID: -1}
	st, err := Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			m.Exists = false
			return m, nil
		}
		return m, err
	}
	m.Exists = true
	m.Mode = st.Mode().Perm()
	if st.Mode()&os.ModeSymlink != 0 {
		m.IsSymlink = true
		if tgt, e := ReadLink(path); e == nil {
			m.LinkTarget = tgt
		}
	}
	uid, gid, ok := ownerFromFileInfo(st)
	if ok {
		m.UID, m.GID = uid, gid
	}
	return m, nil
}

// SnapshotFile copies contents + metadata into snapDir under name (e.g. "tls.key").
func SnapshotFile(path, snapDir, name string) (*Snapshot, error) {
	if err := os.MkdirAll(snapDir, 0o700); err != nil {
		return nil, err
	}
	meta, err := CaptureMeta(path)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{
		Meta:     meta,
		MetaFile: filepath.Join(snapDir, name+".meta.json"),
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.MetaFile, append(raw, '\n'), 0o600); err != nil {
		return nil, err
	}
	if !meta.Exists || meta.IsSymlink {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s.DataFile = filepath.Join(snapDir, name+".data")
	if err := os.WriteFile(s.DataFile, b, 0o600); err != nil {
		return nil, err
	}
	return s, nil
}

// Restore applies a snapshot: absent → remove dest; present → write content + owner/mode.
func Restore(s *Snapshot) error {
	if s == nil {
		return nil
	}
	meta := s.Meta
	if s.MetaFile != "" {
		if b, err := os.ReadFile(s.MetaFile); err == nil {
			var m Meta
			if json.Unmarshal(b, &m) == nil {
				meta = m
			}
		}
	}
	if !meta.Exists {
		_ = os.Remove(meta.Path)
		return nil
	}
	if meta.IsSymlink {
		_ = os.Remove(meta.Path)
		if err := os.Symlink(meta.LinkTarget, meta.Path); err != nil {
			return err
		}
		return ApplyOwnerMode(meta.Path, meta.UID, meta.GID, meta.Mode)
	}
	data := s.DataFile
	if data == "" {
		return fmt.Errorf("filemeta: snapshot for %s missing data file", meta.Path)
	}
	b, err := os.ReadFile(data)
	if err != nil {
		return err
	}
	mode := meta.Mode
	if mode == 0 {
		mode = 0o600
	}
	if err := AtomicWrite(meta.Path, b, mode, meta.UID, meta.GID); err != nil {
		return err
	}
	return nil
}

// AtomicWrite writes data via temp+rename then applies uid/gid/mode.
// If uid/gid are -1, preserves existing destination owner when present, else service user when resolvable.
func AtomicWrite(path string, data []byte, mode os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if uid < 0 || gid < 0 {
		if prev, err := CaptureMeta(path); err == nil && prev.Exists && prev.UID >= 0 {
			uid, gid = prev.UID, prev.GID
		} else if suid, sgid, err := LookupServiceIDs(); err == nil {
			uid, gid = suid, sgid
		}
	}
	tmp := path + ".filemeta.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	_ = os.Chmod(tmp, mode)
	// Prefer chown on temp before rename so final inode is correct even if later chown fails briefly.
	if uid >= 0 && gid >= 0 {
		if err := Chown(tmp, uid, gid); err != nil {
			// Still rename; try again on final path.
			_ = err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return ApplyOwnerMode(path, uid, gid, mode)
}

// ApplyOwnerMode sets uid/gid (when >=0) and permission bits.
func ApplyOwnerMode(path string, uid, gid int, mode os.FileMode) error {
	if mode != 0 {
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("filemeta: chmod %s: %w", path, err)
		}
	}
	if uid >= 0 && gid >= 0 {
		if err := Chown(path, uid, gid); err != nil {
			return fmt.Errorf("filemeta: chown %s: %w", path, err)
		}
	}
	return nil
}

// EnforceRuntimeTLS applies the production TLS ownership contract under stateDir.
func EnforceRuntimeTLS(stateDir string) error {
	uid, gid, err := LookupServiceIDs()
	if err != nil {
		uid, gid = -1, -1
	}
	if st, err := Lstat(stateDir); err == nil && st.IsDir() {
		_ = ApplyOwnerMode(stateDir, uid, gid, RuntimeStateDirMode)
	}
	cert := filepath.Join(stateDir, "tls.crt")
	key := filepath.Join(stateDir, "tls.key")
	var first error
	if _, err := os.Stat(cert); err == nil {
		if err := ApplyOwnerMode(cert, uid, gid, RuntimeTLSCertMode); err != nil && first == nil {
			first = err
		}
	}
	if _, err := os.Stat(key); err == nil {
		if err := ApplyOwnerMode(key, uid, gid, RuntimeTLSKeyMode); err != nil && first == nil {
			first = err
		}
		_ = os.Chmod(key, RuntimeTLSKeyMode)
	}
	return first
}

// KeyWorldReadable reports whether other-read is set (tests).
func KeyWorldReadable(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return st.Mode().Perm()&0o004 != 0, nil
}
