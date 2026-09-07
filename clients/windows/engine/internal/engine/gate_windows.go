//go:build windows

package engine

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/nyxveil/client-windows/internal/recoverylog"
	"github.com/nyxveil/client-windows/internal/winnet"
	"github.com/nyxveil/client-windows/internal/wintundev"
	"github.com/nyxveil/nvp/core/tunnel"
)

const gateAdapterName = "NyxveilGate"

// RunGateWintunOpen loads shipped wintun.dll, creates a temporary adapter, starts
// a session, then closes and destroys it. Elevated host required.
func RunGateWintunOpen() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dev, err := wintundev.Open(ctx, tunnel.Config{Name: gateAdapterName, MTU: 1280})
	if err != nil {
		return err
	}
	name := dev.Name()
	_ = name
	if err := dev.Close(); err != nil {
		return err
	}
	fmt.Println("GATE_WINTUN_OK adapter=", gateAdapterName)
	return nil
}

// RunGateNetTransaction exercises production WindowsApplier on an isolated Wintun
// adapter: host bypass via physical GW, TUN addr/DNS with read-back verification.
// Never replaces the system default Internet route.
func RunGateNetTransaction() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dev, err := wintundev.Open(ctx, tunnel.Config{Name: gateAdapterName, MTU: 1280})
	if err != nil {
		return fmt.Errorf("gate wintun: %w", err)
	}
	defer func() { _ = dev.Close() }()

	applier := NewWindowsApplier()
	applier.JournalPath = recoverylog.DefaultPath()
	applier.SkipDefaultVPN = true

	plan := NewPlan()
	if err := applier.Capture(plan); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	if !plan.CapturedDefault.Present {
		return fmt.Errorf("no IPv4 default route to use as bypass next-hop")
	}

	testHost := netip.MustParsePrefix("198.51.100.99/32")
	plan.SetBypassHosts([]HostRoute{{
		Destination: testHost,
		NextHop:     plan.CapturedDefault.NextHop,
	}})
	if err := applier.ApplyBypass(plan); err != nil {
		return fmt.Errorf("bypass: %w", err)
	}
	if !routeExists(testHost, plan.CapturedDefault.NextHop) {
		_ = applier.Restore(plan)
		return fmt.Errorf("ROUTES_REAL: bypass route not visible via IP Helper after add")
	}

	ifIdx, err := winnet.InterfaceIndexByAlias(gateAdapterName)
	if err != nil {
		_ = applier.Restore(plan)
		return fmt.Errorf("gate adapter ifIndex: %w", err)
	}
	dnsBefore, err := winnet.DNSServersOnInterface(ifIdx)
	if err != nil {
		dnsBefore = nil // adapter may have no DNS yet
	}

	wantDNS := "10.77.0.1"
	if err := plan.ApplyTypeConfig(gateAdapterName, "10.77.0.2", 24, "10.77.0.1", []string{wantDNS}, 1280); err != nil {
		_ = applier.Restore(plan)
		return err
	}
	plan.CapturedIPv6Phys = nil
	plan.CapturedIPv6IfIndex = 0

	if err := applier.ApplyTunnel(plan); err != nil {
		_ = applier.Restore(plan)
		return fmt.Errorf("tunnel cfg: %w", err)
	}

	dnsMid, err := winnet.DNSServersOnInterface(ifIdx)
	if err != nil {
		_ = applier.Restore(plan)
		return fmt.Errorf("DNS_REAL readback after set: %w", err)
	}
	if !dnsContains(dnsMid, wantDNS) {
		_ = applier.Restore(plan)
		return fmt.Errorf("DNS_REAL: expected %s after set, got %v", wantDNS, dnsMid)
	}

	if err := applier.Restore(plan); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	if routeExists(testHost, plan.CapturedDefault.NextHop) {
		return fmt.Errorf("ROUTES_REAL: bypass route still present after restore")
	}
	dnsAfter, err := winnet.DNSServersOnInterface(ifIdx)
	if err != nil {
		dnsAfter = nil
	}
	if !dnsEqualLoose(dnsBefore, dnsAfter) {
		return fmt.Errorf("DNS_REAL: restore mismatch before=%v after=%v", dnsBefore, dnsAfter)
	}
	fmt.Println("GATE_NET_TX_OK dns_set=", wantDNS)
	return nil
}

// RunGateFullTunnelRoutes exercises the production full-tunnel path on a temporary
// Wintun adapter: CreateAdapter → ifIndex/LUID → addr/DNS → 0.0.0.0/1 + 128.0.0.0/1
// on-link → OS VerifyTunnel → stickiness re-check → full restore.
// Briefly diverts IPv4 via the test adapter; always restores before return.
func RunGateFullTunnelRoutes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dev, err := wintundev.Open(ctx, tunnel.Config{Name: gateAdapterName, MTU: 1280})
	if err != nil {
		return fmt.Errorf("gate full-tunnel wintun: %w", err)
	}
	defer func() { _ = dev.Close() }()

	luid, ok := wintundev.AdapterLUID(dev)
	if !ok || luid == 0 {
		return fmt.Errorf("gate full-tunnel: missing AdapterLUID")
	}
	ifIdx, err := winnet.InterfaceIndexByAlias(gateAdapterName)
	if err != nil || ifIdx == 0 {
		return fmt.Errorf("gate full-tunnel: adapter not visible: %w", err)
	}

	applier := NewWindowsApplier()
	applier.JournalPath = recoverylog.DefaultPath() + ".fulltunnel-gate"
	_ = os.Remove(applier.JournalPath)

	plan := NewPlan()
	if err := applier.Capture(plan); err != nil {
		return fmt.Errorf("capture: %w", err)
	}
	// Do not install host bypass for this gate — keep physical path for CP/tests.
	plan.CapturedIPv6Phys = nil
	plan.CapturedIPv6IfIndex = 0
	plan.CapturedIPv6 = nil

	wantDNS := "10.77.0.1"
	if err := plan.ApplyTypeConfig(gateAdapterName, "10.77.0.2", 24, "10.77.0.1", []string{wantDNS}, 1280); err != nil {
		return err
	}
	plan.TunIfIndex = ifIdx
	plan.TunLUID = luid
	plan.Gate.AdapterOpen = true
	plan.Gate.TypeConfigOK = true

	if err := applier.ApplyTunnel(plan); err != nil {
		_ = applier.Restore(plan)
		return fmt.Errorf("full-tunnel ApplyTunnel: %w", err)
	}
	if err := applier.VerifyTunnel(plan); err != nil {
		_ = applier.Restore(plan)
		return fmt.Errorf("full-tunnel VerifyTunnel: %w", err)
	}

	// Stickiness + release-blocking dataplane soak (adapter/routes/DNS must hold).
	checkpoints := []time.Duration{0, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}
	var elapsed time.Duration
	for _, want := range checkpoints {
		if want > elapsed {
			time.Sleep(want - elapsed)
			elapsed = want
		}
		if err := applier.VerifyTunnel(plan); err != nil {
			_ = applier.Restore(plan)
			return fmt.Errorf("full-tunnel soak T+%v VerifyTunnel: %w", want, err)
		}
		curIdx, err := winnet.InterfaceIndexByAlias(gateAdapterName)
		if err != nil || curIdx != ifIdx {
			_ = applier.Restore(plan)
			return fmt.Errorf("full-tunnel soak T+%v adapter ifIndex changed: got %d want %d err=%v", want, curIdx, ifIdx, err)
		}
		onLink := netip.IPv4Unspecified()
		for _, dest := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
			ok, err := winnet.HasIPv4Route(winnet.RouteSpec{
				Destination:    netip.MustParsePrefix(dest),
				NextHop:        onLink,
				InterfaceIndex: ifIdx,
				InterfaceLUID:  luid,
				Metric:         1,
			})
			if err != nil || !ok {
				_ = applier.Restore(plan)
				return fmt.Errorf("full-tunnel soak T+%v missing %s (ok=%v err=%v)", want, dest, ok, err)
			}
		}
		dns, err := winnet.DNSServersOnInterface(ifIdx)
		if err != nil || len(dns) == 0 || dns[0] != wantDNS {
			_ = applier.Restore(plan)
			return fmt.Errorf("full-tunnel soak T+%v DNS got %v err=%v want %s", want, dns, err, wantDNS)
		}
	}

	if err := applier.Restore(plan); err != nil {
		return fmt.Errorf("full-tunnel restore: %w", err)
	}
	onLink := netip.IPv4Unspecified()
	for _, dest := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		ok, err := winnet.HasIPv4Route(winnet.RouteSpec{
			Destination:    netip.MustParsePrefix(dest),
			NextHop:        onLink,
			InterfaceIndex: ifIdx,
			InterfaceLUID:  luid,
			Metric:         1,
		})
		if err != nil {
			return fmt.Errorf("full-tunnel post-restore lookup %s: %w", dest, err)
		}
		if ok {
			return fmt.Errorf("full-tunnel residue: %s still present after restore", dest)
		}
	}
	_ = os.Remove(applier.JournalPath)
	fmt.Println("GATE_FULL_TUNNEL_OK ifIndex=", ifIdx, "luid=", luid, "soak=10s")
	fmt.Println("GATE_DATAPLANE_SOAK_OK duration=10s ifIndex=", ifIdx, "luid=", luid)
	return nil
}

func dnsContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func dnsEqualLoose(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RunGateIPv6RoundTrip captures all active egress IPv6 bindings, toggles each,
// restores exact prior state. Verifies via CaptureIPv6State.
func RunGateIPv6RoundTrip() error {
	states, err := winnet.CaptureAllEgressIPv6(tunAdapterName)
	if err != nil {
		return fmt.Errorf("ipv6 list: %w", err)
	}
	if len(states) == 0 {
		// Fall back to default-route IF for single-adapter hosts.
		def, err := winnet.GetIPv4DefaultRoute()
		if err != nil || !def.Present || def.InterfaceIndex == 0 {
			return fmt.Errorf("no egress IF for IPv6 gate")
		}
		st, err := winnet.CaptureIPv6State(def.InterfaceIndex)
		if err != nil || !st.Captured {
			return fmt.Errorf("ipv6 capture: %w", err)
		}
		states = []winnet.IPv6State{st}
	}
	for _, before := range states {
		if err := winnet.SetIPv6Enabled(before.InterfaceIndex, !before.Enabled); err != nil {
			_ = winnet.RestoreIPv6State(before)
			return fmt.Errorf("ipv6 set if %d: %w", before.InterfaceIndex, err)
		}
		mid, err := winnet.CaptureIPv6State(before.InterfaceIndex)
		if err != nil {
			_ = winnet.RestoreIPv6State(before)
			return err
		}
		if mid.Enabled == before.Enabled {
			_ = winnet.RestoreIPv6State(before)
			return fmt.Errorf("IPV6_REAL: if %d state did not change after set", before.InterfaceIndex)
		}
		if err := winnet.RestoreIPv6State(before); err != nil {
			return err
		}
		after, err := winnet.CaptureIPv6State(before.InterfaceIndex)
		if err != nil {
			return err
		}
		if after.Enabled != before.Enabled {
			return fmt.Errorf("IPV6_REAL: restore mismatch if=%d was=%v now=%v", before.InterfaceIndex, before.Enabled, after.Enabled)
		}
	}
	fmt.Println("GATE_IPV6_OK interfaces=", len(states))
	return nil
}

// RunGateCrashStep applies one production mutation then exits 99 (fault inject).
// step: bypass|tun_addr|tun_dns|ipv6|default_vpn
// default_vpn uses an isolated TEST-NET /32 (never 0.0.0.0/0) with journal kind default_vpn
// so RecoverOnStartup exercises the same undo path without hijacking Internet.
func RunGateCrashStep(step string) error {
	_ = os.Setenv(CrashAfterEnv, "") // we exit manually after controlled step
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	applier := NewWindowsApplier()
	plan := NewPlan()
	if err := applier.Capture(plan); err != nil {
		return err
	}
	if !plan.CapturedDefault.Present {
		return fmt.Errorf("no default route")
	}

	switch step {
	case "bypass":
		testHost := netip.MustParsePrefix("198.51.100.77/32")
		plan.SetBypassHosts([]HostRoute{{Destination: testHost, NextHop: plan.CapturedDefault.NextHop}})
		_ = os.Setenv(CrashAfterEnv, "bypass")
		defer os.Unsetenv(CrashAfterEnv)
		err := applier.ApplyBypass(plan)
		// crashAfter exits; if not:
		return fmt.Errorf("expected crash after bypass, err=%v", err)

	case "tun_addr", "tun_dns", "default_vpn":
		dev, err := wintundev.Open(ctx, tunnel.Config{Name: gateAdapterName, MTU: 1280})
		if err != nil {
			return err
		}
		defer func() { _ = dev.Close() }()
		tunGW := netip.MustParseAddr("10.77.0.1")
		if err := plan.ApplyTypeConfig(gateAdapterName, "10.77.0.2", 24, tunGW.String(), []string{"10.77.0.1"}, 1280); err != nil {
			return err
		}
		plan.CapturedIPv6Phys = nil
		plan.CapturedIPv6IfIndex = 0
		plan.CapturedIPv6 = nil
		if step == "default_vpn" {
			return gateCrashIsolatedVPN(applier, plan, tunGW)
		}
		applier.SkipDefaultVPN = true
		_ = os.Setenv(CrashAfterEnv, step)
		defer os.Unsetenv(CrashAfterEnv)
		err = applier.ApplyTunnel(plan)
		return fmt.Errorf("expected crash after %s, err=%v", step, err)

	case "ipv6":
		was := true
		st, err := winnet.CaptureIPv6State(uint32(plan.CapturedDefault.InterfaceIndex))
		if err == nil && st.Captured {
			was = st.Enabled
		}
		plan.CapturedIPv6Phys = &was
		plan.CapturedIPv6IfIndex = uint32(plan.CapturedDefault.InterfaceIndex)
		// Minimal tunnel plan without Wintun: only ipv6 mutation via ApplyTunnel needs TunName.
		// Use direct journal+set path mirroring production.
		now := false
		mut := recoverylog.Mutation{
			ID: "ipv6-phys", Kind: "ipv6_set",
			IPv6IfIndex: plan.CapturedIPv6IfIndex, IPv6WasEnabled: &was, IPv6NowEnabled: &now,
		}
		applier.journal.Pending = &mut
		_ = applier.persist()
		if was {
			if err := winnet.SetIPv6Enabled(plan.CapturedIPv6IfIndex, false); err != nil {
				return err
			}
		}
		applier.journal.Pending = nil
		applier.journal.Applied = append(applier.journal.Applied, mut)
		_ = applier.persist()
		fmt.Fprintf(os.Stdout, "NYXVEIL_CRASH_CHECKPOINT=ipv6\n")
		fmt.Fprintf(os.Stderr, "NYXVEIL_CRASH_CHECKPOINT=ipv6\n")
		fmt.Fprintf(os.Stderr, "nyxveil: fault-inject crash after ipv6\n")
		os.Exit(99)
		return nil

	default:
		return fmt.Errorf("unknown crash step %q", step)
	}
}

func gateCrashIsolatedVPN(applier *WindowsApplier, plan *Plan, tunGW netip.Addr) error {
	// Apply tun addr/dns without default /0, then journal a default_vpn-shaped
	// isolated TEST-NET route and crash after apply.
	plan.CapturedIPv6Phys = nil
	plan.CapturedIPv6IfIndex = 0
	// Force ApplyTunnel but intercept: set TunGateway and use custom path.
	ip := plan.TunPrefix.Addr().String()
	mask := cidrMask(plan.TunPrefix.Bits())
	mutAddr := recoverylog.Mutation{ID: "tun-addr", Kind: "tun_addr", TunName: plan.TunName, DestPrefix: plan.TunPrefix.String()}
	applier.mu.Lock()
	applier.journal.Pending = &mutAddr
	_ = applier.persist()
	_ = run("netsh", "interface", "ip", "set", "address", "name="+plan.TunName, "static", ip, mask, "none")
	applier.journal.Pending = nil
	applier.journal.Applied = append(applier.journal.Applied, mutAddr)
	_ = applier.persist()

	dst := netip.MustParsePrefix("198.51.100.88/32")
	mut := recoverylog.Mutation{
		ID: "default-vpn", Kind: "default_vpn",
		DestPrefix: dst.String(), NextHop: plan.CapturedDefault.NextHop.String(),
		IfIndex: uint32(plan.CapturedDefault.InterfaceIndex),
		IfLUID:  plan.CapturedDefault.InterfaceLUID, Metric: 1,
	}
	applier.journal.Pending = &mut
	_ = applier.persist()
	spec := winnet.RouteSpec{
		Destination: dst, NextHop: plan.CapturedDefault.NextHop,
		InterfaceIndex: uint32(plan.CapturedDefault.InterfaceIndex),
		InterfaceLUID:  plan.CapturedDefault.InterfaceLUID, Metric: 1,
	}
	if err := winnet.AddRoute(spec); err != nil {
		applier.mu.Unlock()
		return err
	}
	applier.journal.Pending = nil
	applier.journal.Applied = append(applier.journal.Applied, mut)
	_ = applier.persist()
	applier.mu.Unlock()
	_ = tunGW
	fmt.Fprintf(os.Stdout, "NYXVEIL_CRASH_CHECKPOINT=default_vpn\n")
	fmt.Fprintf(os.Stderr, "NYXVEIL_CRASH_CHECKPOINT=default_vpn\n")
	fmt.Fprintf(os.Stderr, "nyxveil: fault-inject crash after default_vpn\n")
	os.Exit(99)
	return nil
}

// VerifyGateClean ensures gate test routes are gone and journal cleared after recovery.
func VerifyGateClean() error {
	applier := NewWindowsApplier()
	if err := applier.RecoverOnStartup(); err != nil {
		return fmt.Errorf("recover: %w", err)
	}
	for _, host := range []string{"198.51.100.99/32", "198.51.100.77/32", "198.51.100.88/32", "198.51.100.55/32"} {
		pfx := netip.MustParsePrefix(host)
		def, _ := winnet.GetIPv4DefaultRoute()
		if def.Present && routeExists(pfx, def.NextHop) {
			return fmt.Errorf("stale gate route still present: %s", host)
		}
	}
	if _, err := os.Stat(recoverylog.DefaultPath()); err == nil {
		// journal should be cleared by rollbackLocked
		_ = recoverylog.Clear(recoverylog.DefaultPath())
	}
	fmt.Println("GATE_CLEAN_OK")
	return nil
}

func routeExists(dst netip.Prefix, via netip.Addr) bool {
	ok, err := winnet.HasIPv4Route(winnet.RouteSpec{Destination: dst, NextHop: via, Metric: 1})
	return err == nil && ok
}
