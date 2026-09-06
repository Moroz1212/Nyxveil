//go:build windows

package recoverylog

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func rejectReparse(path string) error {
	pathp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(pathp)
	if err != nil {
		if err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_PATH_NOT_FOUND {
			return nil
		}
		return err
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("recoverylog: refusing reparse point at %s", path)
	}
	return nil
}

func assertSafeJournalPaths(path string) error {
	dir := filepathDir(path)
	if err := rejectReparse(dir); err != nil {
		return err
	}
	if err := rejectReparse(path); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := rejectReparse(tmp); err != nil {
		return err
	}
	return nil
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '\\' || path[i] == '/' {
			if i == 0 {
				return path[:1]
			}
			return path[:i]
		}
	}
	return "."
}

// ensure parent exists without following reparse.
func ensureJournalDir(path string) error {
	dir := filepathDir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return rejectReparse(dir)
}
