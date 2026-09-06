//go:build !windows

package engine

import (
	nvpwin "github.com/nyxveil/nvp/core/platform/windows"
)

// NewTunFactory wraps Frozen Core Factory (no Wintun bind on non-Windows).
func NewTunFactory() *TunFactory {
	return &TunFactory{inner: nvpwin.NewFactory()}
}
