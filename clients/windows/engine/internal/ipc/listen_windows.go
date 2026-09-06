//go:build windows

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	// AuthorizedSIDFileName is written under ProgramData with SYSTEM+Admins ACL only.
	AuthorizedSIDFileName = "authorized-user.sid"
)

// AuthorizedSIDPath returns %PROGRAMDATA%\Nyxveil\Client\authorized-user.sid
func AuthorizedSIDPath() string {
	base := os.Getenv("PROGRAMDATA")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "Nyxveil", "Client", AuthorizedSIDFileName)
}

// ClientDataDir is %PROGRAMDATA%\Nyxveil\Client
func ClientDataDir() string {
	return filepath.Dir(AuthorizedSIDPath())
}

// ListenSecure creates the Nyxveil named pipe.
// DACL: LocalSystem + Builtin Administrators + provisioned interactive user SID.
// Never Everyone / Authenticated Users / Interactive.
func ListenSecure(pipeName string) (net.Listener, error) {
	if pipeName == "" {
		pipeName = PipeName
	}
	sddl, err := authorizedPipeSDDL()
	if err != nil {
		return nil, err
	}
	cfg := &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}
	ln, err := winio.ListenPipe(pipeName, cfg)
	if err != nil {
		return nil, fmt.Errorf("ipc: ListenPipe: %w", err)
	}
	return ln, nil
}

func authorizedPipeSDDL() (string, error) {
	sidSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return "", err
	}
	sidAdmins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return "", err
	}
	sidUser, err := loadAuthorizedUserSID()
	if err != nil {
		return "", err
	}
	if isBroadSID(sidUser) {
		return "", fmt.Errorf("ipc: authorized SID is too broad (%s)", sidUser.String())
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)(A;;GA;;;%s)",
		sidSystem.String(), sidAdmins.String(), sidUser.String()), nil
}

func loadAuthorizedUserSID() (*windows.SID, error) {
	path := AuthorizedSIDPath()
	if err := rejectReparsePoint(path); err != nil {
		return nil, err
	}
	if err := rejectReparsePoint(filepath.Dir(path)); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ipc: authorized user SID not provisioned (%s): %w — run installer or nyxveil-service -provision-sid", path, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil, fmt.Errorf("ipc: empty authorized SID file")
	}
	sid, err := windows.StringToSid(s)
	if err != nil {
		return nil, fmt.Errorf("ipc: parse authorized SID: %w", err)
	}
	return sid, nil
}

// ProvisionAuthorizedSID writes the given SID (or current process user if empty).
// When the process can set ACLs (Admin/System), the file DACL is locked to
// SYSTEM + Administrators only. Ordinary users cannot later overwrite it.
func ProvisionAuthorizedSID(sidStr string) error {
	if sidStr == "" {
		sid, err := processUserSID()
		if err != nil {
			return err
		}
		sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
		if sid.Equals(sys) {
			return fmt.Errorf("ipc: refusing to provision LocalSystem as authorized GUI SID")
		}
		sidStr = sid.String()
	}
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return err
	}
	if isBroadSID(sid) {
		return fmt.Errorf("ipc: refusing broad SID %s", sidStr)
	}
	dir := ClientDataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := rejectReparsePoint(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	// When elevated, harden directory before writing SID (installer / SYSTEM path).
	if windows.GetCurrentProcessToken().IsElevated() {
		if err := ProtectClientDataDir(); err != nil {
			return err
		}
	}
	path := AuthorizedSIDPath()
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ipc: refusing symlink at %s", path)
		}
		if err := rejectReparsePoint(path); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(sidStr+"\n"), 0o600); err != nil {
		return err
	}
	if err := rejectReparsePoint(tmp); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Harden ACL only when elevated (installer lock pass / SYSTEM). A non-elevated
	// Administrators token must not lock itself out under UAC filtering.
	if windows.GetCurrentProcessToken().IsElevated() {
		if err := ProtectAuthorizedSIDFileACL(path); err != nil {
			return fmt.Errorf("ipc: write SID ok but ACL lock failed: %w", err)
		}
	}
	return nil
}

// LockAuthorizedSIDACL is an elevated-only step: harden directory + SID file ACL.
func LockAuthorizedSIDACL() error {
	if err := ProtectClientDataDir(); err != nil {
		return err
	}
	path := AuthorizedSIDPath()
	if err := rejectReparsePoint(path); err != nil {
		return err
	}
	return ProtectAuthorizedSIDFileACL(path)
}

func rejectReparsePoint(path string) error {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(pathp)
	if err != nil {
		if err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_PATH_NOT_FOUND {
			return os.ErrNotExist
		}
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("ipc: refusing reparse point / symlink at %s", path)
	}
	return nil
}

func processUserSID() (*windows.SID, error) {
	token := windows.GetCurrentProcessToken()
	tokUser, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return tokUser.User.Sid, nil
}

func isBroadSID(sid *windows.SID) bool {
	checks := []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,
		windows.WinAuthenticatedUserSid,
		windows.WinInteractiveSid,
		windows.WinBuiltinUsersSid,
	}
	for _, k := range checks {
		wk, err := windows.CreateWellKnownSid(k)
		if err != nil {
			continue
		}
		if sid.Equals(wk) {
			return true
		}
	}
	return false
}

// SDDLForTest exposes SDDL construction for unit tests (requires provisioned SID).
func SDDLForTest() (string, error) { return authorizedPipeSDDL() }

// SDDLForSIDs builds SDDL for tests with an explicit user SID (no file I/O).
func SDDLForSIDs(userSID string) (string, error) {
	sidSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return "", err
	}
	sidAdmins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return "", err
	}
	sidUser, err := windows.StringToSid(userSID)
	if err != nil {
		return "", err
	}
	if isBroadSID(sidUser) {
		return "", fmt.Errorf("broad SID")
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)(A;;GA;;;%s)",
		sidSystem.String(), sidAdmins.String(), sidUser.String()), nil
}

// ListenSecureWithSDDL is for tests that inject a constructed SDDL.
func ListenSecureWithSDDL(pipeName, sddl string) (net.Listener, error) {
	cfg := &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    65536,
		OutputBufferSize:   65536,
	}
	return winio.ListenPipe(pipeName, cfg)
}
