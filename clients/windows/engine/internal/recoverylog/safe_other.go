//go:build !windows

package recoverylog

import (
	"os"
	"path/filepath"
)

func ensureJournalDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}

func rejectReparse(path string) error { return nil }

func assertSafeJournalPaths(path string) error { return nil }
