//go:build windows

package ipc

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// protectPathACL sets a protected DACL: SYSTEM + Administrators Full Control only.
// For directories, OICI inheritance is set so new children do not pick up Users write from parents.
func protectPathACL(path string) error {
	sidSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	sidAdmins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	// D:P = DACL protected (no inherit from parent). OICI applies to directory children.
	sddl := fmt.Sprintf("D:P(A;OICI;FA;;;%s)(A;OICI;FA;;;%s)", sidSystem.String(), sidAdmins.String())
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("ipc: SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
	if err != nil {
		return fmt.Errorf("ipc: SetNamedSecurityInfo %s: %w", path, err)
	}
	return nil
}

// ProtectClientDataDir creates %ProgramData%\Nyxveil\Client if needed and sets a
// protected DACL: SYSTEM + Administrators Full Control only (no inherited Users write).
func ProtectClientDataDir() error {
	dir := ClientDataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := rejectReparsePoint(dir); err != nil {
		return err
	}
	if err := protectPathACL(dir); err != nil {
		return err
	}
	// Harden existing children (journal, SID file, flags) that may still carry inherited ACEs.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		_ = protectPathACL(filepath.Join(dir, e.Name()))
	}
	return nil
}

// ProtectAuthorizedSIDFileACL sets DACL: SYSTEM + Administrators Full Control; no Users/Everyone.
func ProtectAuthorizedSIDFileACL(path string) error {
	if err := rejectReparsePoint(path); err != nil {
		return err
	}
	return protectPathACL(path)
}

// WriteSIDToken writes the current process user SID to a caller-owned path (e.g. {tmp}).
// Used by ExecAsOriginalUser so elevated install can place the SID into protected ProgramData.
func WriteSIDToken(tokenPath string) error {
	sid, err := processUserSID()
	if err != nil {
		return err
	}
	sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if sid.Equals(sys) {
		return fmt.Errorf("ipc: refusing LocalSystem as authorized GUI SID")
	}
	if isBroadSID(sid) {
		return fmt.Errorf("ipc: refusing broad SID %s", sid.String())
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(tokenPath, []byte(sid.String()+"\n"), 0o600)
}

// InstallAuthorizedSIDFromToken reads a SID token (from original user) and writes it
// into the protected Client data directory. Elevated only.
func InstallAuthorizedSIDFromToken(tokenPath string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("ipc: InstallAuthorizedSIDFromToken requires elevation")
	}
	if err := rejectReparsePoint(tokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	b, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	sidStr := string(bytesTrimSpace(b))
	if err := ProtectClientDataDir(); err != nil {
		return err
	}
	return ProvisionAuthorizedSID(sidStr)
}

func bytesTrimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return b[i:j]
}
