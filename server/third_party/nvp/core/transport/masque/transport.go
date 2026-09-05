// Package masque reserves a future CONNECT-UDP / MASQUE extension point.
// MASQUE is NOT part of NVP/1 implemented transports. Do not register it
// in production registries and do not advertise it as available.
package masque

// ExtensionName is the future profile name (not registered in NVP/1).
const ExtensionName = "masque-connect-udp"

// Capabilities describes planned MASQUE requirements for a future release.
type Capabilities struct {
	RequiresHTTP3  bool
	RequiresTLS13  bool
	Method         string
	TargetTemplate string
}

// DefaultCapabilities returns planned MASQUE profile metadata.
func DefaultCapabilities() Capabilities {
	return Capabilities{
		RequiresHTTP3:  true,
		RequiresTLS13:  true,
		Method:         "CONNECT-UDP",
		TargetTemplate: ":authority",
	}
}

// Available reports whether MASQUE is implemented in this build.
func Available() bool { return false }
