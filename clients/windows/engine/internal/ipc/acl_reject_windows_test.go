//go:build windows

package ipc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/nyxveil/client-windows/internal/ipc"
	"golang.org/x/sys/windows"
)

// Unauthorized SID (well-known Null SID is broad? Use a synthetic non-current SID).
// S-1-5-21-0-0-0-1001 is an arbitrary domain user SID that is not the current process.
func TestListenSecureRejectsUnauthorizedUser(t *testing.T) {
	other := "S-1-5-21-3623811015-3361044348-30300820-1013"
	sddl, err := ipc.SDDLForSIDs(other)
	if err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\NyxveilReject` + time.Now().Format("150405.000")
	ln, err := ipc.ListenSecureWithSDDL(name, sddl)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	_, err = winio.DialPipe(name, nil)
	if err == nil {
		t.Fatal("expected ACCESS_DENIED for non-authorized user SID")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "access") && !strings.Contains(msg, "denied") && !strings.Contains(msg, "5") {
		t.Fatalf("unexpected dial error (want access denied): %v", err)
	}
}

func TestSDDLIncludesExactProvisionedUserNotProcessOnly(t *testing.T) {
	t.Setenv("PROGRAMDATA", t.TempDir())
	if err := ipc.ProvisionAuthorizedSID(""); err != nil {
		t.Fatal(err)
	}
	sddl, err := ipc.SDDLForTest()
	if err != nil {
		t.Fatal(err)
	}
	token := windows.GetCurrentProcessToken()
	tu, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	userSID := tu.User.Sid.String()
	if !strings.Contains(sddl, userSID) {
		t.Fatalf("provisioned user SID missing from SDDL: %s", sddl)
	}
	sys, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if !strings.Contains(sddl, sys.String()) && !strings.Contains(sddl, "SY") {
		t.Fatalf("SYSTEM missing: %s", sddl)
	}
}
