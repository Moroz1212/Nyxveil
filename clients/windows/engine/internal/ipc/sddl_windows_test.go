//go:build windows

package ipc_test

import (
	"strings"
	"testing"

	"github.com/nyxveil/client-windows/internal/ipc"
	"golang.org/x/sys/windows"
)

func TestPipeSDDLExcludesEveryone(t *testing.T) {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl, err := ipc.SDDLForSIDs(tu.User.Sid.String())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sddl, "WD") || strings.Contains(sddl, "World") {
		t.Fatalf("Everyone/World must not appear: %s", sddl)
	}
	if strings.Contains(sddl, "IU") {
		t.Fatalf("Interactive Users must not appear: %s", sddl)
	}
	if !strings.HasPrefix(sddl, "D:P") {
		t.Fatalf("expected protected DACL prefix, got %s", sddl)
	}
	if !strings.Contains(sddl, tu.User.Sid.String()) {
		t.Fatalf("user SID missing: %s", sddl)
	}
}

func TestSDDLRejectsBroadSID(t *testing.T) {
	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ipc.SDDLForSIDs(world.String()); err == nil {
		t.Fatal("expected rejection of Everyone")
	}
}
