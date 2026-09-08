package paths

import "path"

// Default filesystem layout for a production Linux node.
// Destinations that are signed into release manifests MUST use slash-separated
// Unix paths (package path, not filepath) so Windows packaging hosts emit the
// same JSON as Linux nodes expect.
const (
	EtcDir      = "/etc/nyxveil"
	StateDir    = "/var/lib/nyxveil"
	RunDir      = "/run/nyxveil"
	BinDir      = "/usr/local/sbin"
	ShareDir    = "/usr/local/share/nyxveil"
	SysctlFile  = "/etc/sysctl.d/99-nyxveil.conf"
	ServiceUnit = "/etc/systemd/system/nyxveil-server.service"
)

func ServerConfig() string   { return path.Join(EtcDir, "server.json") }
func NodeKey() string        { return path.Join(StateDir, "node.key") }
func TLSCert() string        { return path.Join(StateDir, "tls.crt") }
func TLSKey() string         { return path.Join(StateDir, "tls.key") }
func AppliedConfig() string  { return path.Join(StateDir, "applied-config.json") }
func CommandsState() string  { return path.Join(StateDir, "commands-state.json") }
func ControlSocket() string  { return path.Join(RunDir, "control.sock") }
func BinaryPath() string     { return path.Join(BinDir, "nyxveil-server") }
func PreviousBinary() string { return path.Join(StateDir, "nyxveil-server.prev") }
func RollbackMarker() string { return path.Join(StateDir, "update-rollback") }
func UpdateTransactionDir() string {
	return path.Join(StateDir, "update-transactions")
}
func UpdateTransactionLock() string {
	return path.Join(StateDir, "update-transaction.lock")
}
func ScriptsDir() string     { return path.Join(ShareDir, "scripts") }
func ProductionGate() string { return path.Join(ScriptsDir(), "production-gate.sh") }
func CatalogVerify() string  { return path.Join(BinDir, "nyxveil-catalog-verify") }
func ShareVersion() string   { return path.Join(ShareDir, "VERSION") }
func ShareThirdParty() string {
	return path.Join(ShareDir, "THIRD_PARTY_CORE.md")
}
func UpdateServiceUnit() string {
	return "/etc/systemd/system/nyxveil-update.service"
}
func ManagementPolkitRule() string {
	return "/etc/polkit-1/rules.d/50-nyxveil-management.rules"
}

// DefaultExtraInstallMaps returns asset-name → destination and backup paths for
// every auxiliary file managed by nyxveilctl update.
func DefaultExtraInstallMaps() (dest map[string]string, prev map[string]string) {
	dest = map[string]string{
		"nyxveilctl":                path.Join(BinDir, "nyxveilctl"),
		"nyxveil-catalog-verify":    CatalogVerify(),
		"production-gate":           ProductionGate(),
		"share-version":             ShareVersion(),
		"share-third-party-core":    ShareThirdParty(),
		"nyxveil-update-service":    UpdateServiceUnit(),
		"nyxveil-management-polkit": ManagementPolkitRule(),
	}
	prev = map[string]string{
		"nyxveilctl":                path.Join(StateDir, "nyxveilctl.prev"),
		"nyxveil-catalog-verify":    path.Join(StateDir, "nyxveil-catalog-verify.prev"),
		"production-gate":           path.Join(StateDir, "production-gate.sh.prev"),
		"share-version":             path.Join(StateDir, "share-VERSION.prev"),
		"share-third-party-core":    path.Join(StateDir, "share-THIRD_PARTY_CORE.md.prev"),
		"nyxveil-update-service":    path.Join(StateDir, "nyxveil-update.service.prev"),
		"nyxveil-management-polkit": path.Join(StateDir, "50-nyxveil-management.rules.prev"),
	}
	return dest, prev
}
