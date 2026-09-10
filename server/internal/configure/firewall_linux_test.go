//go:build linux

package configure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirewallValidationFailurePreservesConfiguration(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	t.Setenv("FW_TRACE", trace)
	t.Setenv("PATH", dir)
	for name, body := range map[string]string{
		"nft":       "#!/bin/sh\necho nft \"$@\" >>\"$FW_TRACE\"\nexit 1\n",
		"systemctl": "#!/bin/sh\necho systemctl \"$@\" >>\"$FW_TRACE\"\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := filepath.Join(dir, "rules")
	previous := "previous valid rules\n"
	if err := os.WriteFile(conf, []byte(previous), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyNyxveilFirewall(FirewallOpts{NFTFile: conf}); err == nil {
		t.Fatal("expected validation failure")
	}
	got, err := os.ReadFile(conf)
	if err != nil || string(got) != previous {
		t.Fatalf("previous configuration changed: %q %v", got, err)
	}
	commands, err := os.ReadFile(trace)
	if err != nil || strings.TrimSpace(string(commands)) != "nft --check -f "+conf+".tmp" {
		t.Fatalf("unexpected commands after failed validation: %q %v", commands, err)
	}
	if _, err := os.Stat(conf + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("staging file retained: %v", err)
	}
}

func TestFirewallUnitFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := reloadFirewallUnit(); err == nil {
		t.Fatal("systemctl failure was ignored")
	}
}
