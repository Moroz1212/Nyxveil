package engine

import "fmt"

// DataplaneGate is the fail-closed checklist required before state.Connected.
// Connected is forbidden unless every field is true after TypeConfig + TUN + routes.
type DataplaneGate struct {
	TypeConfigOK   bool
	AdapterOpen    bool
	AddressApplied bool
	RoutesApplied  bool
	DNSApplied     bool
	PumpsStarted   bool
}

// Ready reports whether the dataplane checklist is complete.
func (g DataplaneGate) Ready() bool {
	return g.TypeConfigOK && g.AdapterOpen && g.AddressApplied &&
		g.RoutesApplied && g.DNSApplied && g.PumpsStarted
}

// ErrIncomplete is returned when Connected was requested without a full dataplane.
func (g DataplaneGate) ErrIncomplete() error {
	if g.Ready() {
		return nil
	}
	return fmt.Errorf("engine: dataplane incomplete (typeconfig=%v adapter=%v addr=%v routes=%v dns=%v pumps=%v)",
		g.TypeConfigOK, g.AdapterOpen, g.AddressApplied, g.RoutesApplied, g.DNSApplied, g.PumpsStarted)
}
