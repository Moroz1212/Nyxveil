//go:build !windows

package engine

func GateModeEnabled() bool { return false }
