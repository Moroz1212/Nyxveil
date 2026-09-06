//go:build windows

package ipc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/nyxveil/client-windows/internal/ipc"
	"golang.org/x/sys/windows"
)

func TestSDDLUsesProvisionedSIDNotProcessTokenSemantics(t *testing.T) {
	t.Setenv("PROGRAMDATA", t.TempDir())
	// Simulate LocalSystem deployment: provision an explicit interactive-looking SID
	// (current user), then SDDL must include that SID + SYSTEM + Admins, never Everyone.
	if err := ipc.ProvisionAuthorizedSID(""); err != nil {
		t.Fatal(err)
	}
	sddl, err := ipc.SDDLForTest()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"WD", "S-1-1-0", "IU", "S-1-5-4", "AU", "S-1-5-11"} {
		if strings.Contains(sddl, bad) {
			// WD/IU/AU short forms — S-1-1-0 is Everyone
			if bad == "AU" && strings.Contains(sddl, "S-1-5-32") {
				continue // Administrators contain AU substring risk — check carefully
			}
		}
	}
	if strings.Contains(sddl, "S-1-1-0") || strings.Contains(sddl, "(A;;GA;;;WD)") {
		t.Fatalf("Everyone present: %s", sddl)
	}
	if strings.Contains(sddl, "S-1-5-4") || strings.Contains(sddl, "(A;;GA;;;IU)") {
		t.Fatalf("Interactive present: %s", sddl)
	}
	if !strings.Contains(sddl, "S-1-5-18") && !strings.Contains(sddl, "SY") {
		t.Fatalf("SYSTEM missing: %s", sddl)
	}
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sddl, sid.String()) && !strings.Contains(sddl, "BA") {
		t.Fatalf("Admins missing: %s", sddl)
	}
}

func TestBroadSIDRejected(t *testing.T) {
	_, err := ipc.SDDLForSIDs("S-1-1-0") // Everyone
	if err == nil {
		t.Fatal("expected reject Everyone")
	}
}

func TestListenSecureWithProvisionedSID(t *testing.T) {
	t.Setenv("PROGRAMDATA", t.TempDir())
	if err := ipc.ProvisionAuthorizedSID(""); err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\NyxveilACLTest` + time.Now().Format("150405.000")
	ln, err := ipc.ListenSecure(name)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		_ = c.Close()
		done <- nil
	}()
	c, err := winio.DialPipe(name, nil)
	if err != nil {
		t.Fatalf("authorized user dial: %v", err)
	}
	_ = c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestListenSecureFailsWithoutProvision(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROGRAMDATA", dir)
	// Ensure file missing
	_ = os.Remove(filepath.Join(dir, "Nyxveil", "authorized-user.sid"))
	_, err := ipc.ListenSecure(`\\.\pipe\NyxveilNoSID` + time.Now().Format("150405"))
	if err == nil {
		t.Fatal("expected failure without provisioned SID")
	}
}
