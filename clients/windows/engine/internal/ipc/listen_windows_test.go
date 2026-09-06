//go:build windows

package ipc_test

import (
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/nyxveil/client-windows/internal/ipc"
	"golang.org/x/sys/windows"
)

func TestListenSecureAcceptsLocalConnection(t *testing.T) {
	tok := windows.GetCurrentProcessToken()
	tu, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sddl, err := ipc.SDDLForSIDs(tu.User.Sid.String())
	if err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\NyxveilClientTest` + time.Now().Format("150405.000")
	ln, err := ipc.ListenSecureWithSDDL(name, sddl)
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
		t.Fatalf("DialPipe: %v", err)
	}
	_ = c.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("accept timeout")
	}
}
