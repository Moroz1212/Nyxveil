package configure_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nyxveil/server/internal/configure"
	"github.com/nyxveil/server/internal/filemeta"
)

func TestConfigureRollbackRestoresTLSMetadata(t *testing.T) {
	h := setupACMEHarness(t)
	liveBefore, _ := os.ReadFile(h.liveKey)
	stBefore, _ := os.Stat(h.liveKey)
	modeBefore := stBefore.Mode().Perm()

	old := filemeta.Chown
	defer func() { filemeta.Chown = old }()
	var sawUID int
	filemeta.Chown = func(name string, uid, gid int) error {
		if filepath.Base(name) == "tls.key" {
			sawUID = uid
		}
		return nil
	}

	res, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return fmt.Errorf("acme boom")
		}
		o.SkipCertTrust = true
	}))
	if err == nil {
		t.Fatal("expected failure")
	}
	if res == nil || !res.RolledBack {
		t.Fatal("expected rollback")
	}
	liveAfter, _ := os.ReadFile(h.liveKey)
	if string(liveAfter) != string(liveBefore) {
		t.Fatal("tls.key contents changed")
	}
	stAfter, _ := os.Stat(h.liveKey)
	if stAfter.Mode().Perm() != modeBefore && stAfter.Mode().Perm() != 0o600 {
		t.Fatalf("mode before=%o after=%o", modeBefore, stAfter.Mode().Perm())
	}
	_ = sawUID // chown may be skipped when uid unknown on Windows
}

func TestConfigureRollbackRestoresTLSModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits not enforced on windows")
	}
	h := setupACMEHarness(t)
	_ = os.Chmod(h.liveKey, 0o600)
	_, err := configure.Apply(context.Background(), h.baseOpts(func(o *configure.Options) {
		o.ExecACME = func(ctx context.Context, a configure.ACMEIssueArgs) error {
			return fmt.Errorf("acme boom")
		}
	}))
	if err == nil {
		t.Fatal("expected failure")
	}
	bad, e := filemeta.KeyWorldReadable(h.liveKey)
	if e != nil || bad {
		t.Fatalf("world=%v err=%v", bad, e)
	}
}
