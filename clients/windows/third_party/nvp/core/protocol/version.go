// Package protocol defines NVP/1 version constants used inside authenticated channels only.
package protocol

const (
	// WireVersion is the internal protocol version identifier (NVP/1).
	// Never transmitted as plaintext pre-authentication bytes.
	WireVersion = "NVP/1"

	// MinVersion is the minimum supported protocol version number.
	MinVersion uint16 = 1

	// MaxVersion is the maximum supported protocol version number.
	MaxVersion uint16 = 1

	// CurrentVersion is the active protocol version number.
	CurrentVersion uint16 = 1
)
