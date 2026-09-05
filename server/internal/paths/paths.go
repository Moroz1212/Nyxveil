package paths

import "path/filepath"

// Default filesystem layout for a production Linux node.
const (
	EtcDir      = "/etc/nyxveil"
	StateDir    = "/var/lib/nyxveil"
	RunDir      = "/run/nyxveil"
	BinDir      = "/usr/local/sbin"
	SysctlFile  = "/etc/sysctl.d/99-nyxveil.conf"
	ServiceUnit = "/etc/systemd/system/nyxveil-server.service"
)

func ServerConfig() string   { return filepath.Join(EtcDir, "server.json") }
func NodeKey() string        { return filepath.Join(StateDir, "node.key") }
func TLSCert() string        { return filepath.Join(StateDir, "tls.crt") }
func TLSKey() string         { return filepath.Join(StateDir, "tls.key") }
func AppliedConfig() string  { return filepath.Join(StateDir, "applied-config.json") }
func ControlSocket() string  { return filepath.Join(RunDir, "control.sock") }
func BinaryPath() string     { return filepath.Join(BinDir, "nyxveil-server") }
func PreviousBinary() string { return filepath.Join(StateDir, "nyxveil-server.prev") }
func RollbackMarker() string { return filepath.Join(StateDir, "update-rollback") }
