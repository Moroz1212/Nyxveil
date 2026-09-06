//go:build windows

package engine

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/nyxveil/client-windows/internal/ipc"
	"github.com/nyxveil/client-windows/internal/recoverylog"
	"github.com/nyxveil/client-windows/internal/winnet"
)

// GateModeFlagPath enables test-only IPC gate_apply_isolated (created by elevated gate).
func GateModeFlagPath() string {
	return filepath.Join(ipc.ClientDataDir(), "gate-mode.flag")
}

func GateModeEnabled() bool {
	_, err := os.Stat(GateModeFlagPath())
	return err == nil
}

// ApplyGateIsolatedTransaction applies a safe TEST-NET host route via production
// WindowsApplier and keeps plan on the Manager so SCM Stop → Disconnect restores it.
func (m *Manager) ApplyGateIsolatedTransaction() (netip.Prefix, netip.Addr, error) {
	if !GateModeEnabled() {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("engine: gate mode not enabled")
	}
	m.mu.Lock()
	if m.connecting || m.sess != nil {
		m.mu.Unlock()
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("engine: busy")
	}
	if m.plan != nil {
		_ = m.Routes.Restore(m.plan)
		m.plan = nil
	}
	m.mu.Unlock()

	applier, ok := m.Routes.(*WindowsApplier)
	if !ok {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("engine: WindowsApplier required")
	}
	applier.SkipDefaultVPN = true
	applier.JournalPath = recoverylog.DefaultPath()

	plan := NewPlan()
	if err := applier.Capture(plan); err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}
	if !plan.CapturedDefault.Present {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("no default route")
	}
	testHost := netip.MustParsePrefix("198.51.100.55/32")
	plan.SetBypassHosts([]HostRoute{{
		Destination: testHost,
		NextHop:     plan.CapturedDefault.NextHop,
	}})
	if err := applier.ApplyBypass(plan); err != nil {
		return netip.Prefix{}, netip.Addr{}, err
	}
	if !routeExists(testHost, plan.CapturedDefault.NextHop) {
		_ = applier.Restore(plan)
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("gate isolated route not present after apply")
	}

	m.mu.Lock()
	m.plan = plan
	m.mu.Unlock()
	return testHost, plan.CapturedDefault.NextHop, nil
}

// GateIsolatedMarker returns the TEST-NET route used by ApplyGateIsolatedTransaction.
func GateIsolatedMarker() (netip.Prefix, error) {
	return netip.ParsePrefix("198.51.100.55/32")
}

// VerifyGateIsolatedAbsent checks the service-owned marker route is gone.
func VerifyGateIsolatedAbsent(via netip.Addr) error {
	pfx := netip.MustParsePrefix("198.51.100.55/32")
	if via.IsValid() && routeExists(pfx, via) {
		return fmt.Errorf("stale service-owned gate route still present")
	}
	if def, err := winnet.GetIPv4DefaultRoute(); err == nil && def.Present {
		if routeExists(pfx, def.NextHop) {
			return fmt.Errorf("stale gate route via current default still present")
		}
	}
	return nil
}
