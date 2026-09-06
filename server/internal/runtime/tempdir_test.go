package runtime_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nyxveil-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
		for i := 0; i < 20; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
	return dir
}
