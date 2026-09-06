package updater_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tempDir is a Windows-safe alternative to t.TempDir when updater tests create
// tightly permissioned TLS/prev files that can block RemoveAll cleanup.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nyxveil-upd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		forceWritableTree(dir)
		for i := 0; i < 20; i++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			forceWritableTree(dir)
			time.Sleep(25 * time.Millisecond)
		}
	})
	return dir
}

func forceWritableTree(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		mode := os.FileMode(0o666)
		if info.IsDir() {
			mode = 0o777
		}
		_ = os.Chmod(path, mode)
		return nil
	})
}
