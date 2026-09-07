//go:build windows

package main

import "os"

func lockFileExclusive(f *os.File) error {
	// Windows tests use a best-effort exclusive create; flock is not available.
	_ = f
	return nil
}
