package version

const (
	ServerVersion   = "1.0.1"
	CoreVersion     = "1.0.0"
	ProtocolVersion = "NVP/1"
	ProtocolNumber  = uint16(1)
)

// Build metadata injected via -ldflags when available.
var (
	Commit = "unknown"
	Built  = "unknown"
)
