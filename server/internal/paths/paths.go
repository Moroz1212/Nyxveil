package paths

import "path/filepath"

// Default filesystem layout for a production Linux node.
const (
	EtcDir      = "/etc/nyxveil"
	StateDir    = "/var/lib/nyxveil"
	RunDir      = "/run/nyxveil"
	BinDir      = "/usr/local/sbin"
	ShareDir    = "/usr/local/share/nyxveil"
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
func ScriptsDir() string     { return filepath.Join(ShareDir, "scripts") }
func ProductionGate() string { return filepath.Join(ScriptsDir(), "production-gate.sh") }
func CatalogVerify() string  { return filepath.Join(BinDir, "nyxveil-catalog-verify") }
func ShareVersion() string   { return filepath.Join(ShareDir, "VERSION") }
func ShareThirdParty() string {
	return filepath.Join(ShareDir, "THIRD_PARTY_CORE.md")
}

// DefaultExtraInstallMaps returns asset-name → destination and backup paths for
// every auxiliary file managed by nyxveilctl update.
func DefaultExtraInstallMaps() (dest map[string]string, prev map[string]string) {
	dest = map[string]string{
		"nyxveilctl":               filepath.Join(BinDir, "nyxveilctl"),
		"nyxveil-catalog-verify":   CatalogVerify(),
		"production-gate":          ProductionGate(),
		"share-version":            ShareVersion(),
		"share-third-party-core":   ShareThirdParty(),
	}
	prev = map[string]string{
		"nyxveilctl":               filepath.Join(StateDir, "nyxveilctl.prev"),
		"nyxveil-catalog-verify":   filepath.Join(StateDir, "nyxveil-catalog-verify.prev"),
		"production-gate":          filepath.Join(StateDir, "production-gate.sh.prev"),
		"share-version":            filepath.Join(StateDir, "share-VERSION.prev"),
		"share-third-party-core":   filepath.Join(StateDir, "share-THIRD_PARTY_CORE.md.prev"),
	}
	return dest, prev
}
