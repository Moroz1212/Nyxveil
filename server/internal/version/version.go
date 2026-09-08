package version

const (
	CoreVersion     = "1.0.0"
	ProtocolVersion = "NVP/1"
	ProtocolNumber  = uint16(1)
)

// ServerVersion / CLIVersion are vars so process-level self-update tests can
// build an old ctl via -ldflags -X without forking the tree.
var (
	ServerVersion = "1.1.9"
	CLIVersion    = "1.1.9"
)

// Build metadata injected via -ldflags when available.
var (
	Commit = "unknown"
	Built  = "unknown"
)
