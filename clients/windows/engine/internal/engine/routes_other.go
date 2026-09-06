//go:build !windows

package engine

// RecoverOnStartup is a no-op off Windows.
func (n *NoopApplier) RecoverOnStartup() error { return nil }
