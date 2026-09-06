//go:build windows

package ipc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyxveil/client-windows/internal/ipc"
	"golang.org/x/sys/windows"
)

func TestProtectAuthorizedSIDFileACL_NoEveryone(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires elevated process to apply SYSTEM+Admins ACL")
	}
	dir := t.TempDir()
	t.Setenv("PROGRAMDATA", dir)
	if err := ipc.ProvisionAuthorizedSID(""); err != nil {
		t.Fatal(err)
	}
	path := ipc.AuthorizedSIDPath()
	if err := ipc.ProtectAuthorizedSIDFileACL(path); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	s := sd.String()
	low := strings.ToLower(s)
	if strings.Contains(low, "s-1-1-0") || strings.Contains(s, "(A;;FA;;;WD)") {
		t.Fatalf("Everyone present in ACL: %s", s)
	}
	if strings.Contains(low, "s-1-5-4") || strings.Contains(s, "(A;;FA;;;IU)") {
		t.Fatalf("Interactive present: %s", s)
	}
}

func TestRejectReparsePointOnSIDPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROGRAMDATA", dir)
	client := filepath.Join(dir, "Nyxveil", "Client")
	if err := os.MkdirAll(client, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "evil-target")
	if err := os.WriteFile(target, []byte("S-1-5-21-1-2-3-4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(client, ipc.AuthorizedSIDFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not permitted: %v", err)
	}
	_, err := ipc.SDDLForTest()
	if err == nil {
		t.Fatal("expected reject symlink SID file")
	}
}
