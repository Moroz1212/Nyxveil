//go:build windows

package engine

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/nyxveil/client-windows/internal/recoverylog"
	"github.com/nyxveil/client-windows/internal/winnet"
)

// WindowsApplier mutates routes/DNS/IPv6 via IP Helper + netsh.
// Every OS mutation: journal Pending → OS call → journal Applied (atomic files).
type WindowsApplier struct {
	JournalPath    string
	SkipDefaultVPN bool // gate/tests: configure TUN without replacing system default route
	mu             sync.Mutex
	journal        recoverylog.Journal
}

func NewWindowsApplier() *WindowsApplier {
	return &WindowsApplier{JournalPath: recoverylog.DefaultPath()}
}

func (w *WindowsApplier) persist() error {
	return recoverylog.Write(w.JournalPath, w.journal)
}

func (w *WindowsApplier) Capture(p *Plan) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	def, err := winnet.GetIPv4DefaultRoute()
	if err != nil {
		return err
	}
	p.CapturedDefault = DefaultRouteSnapshot{
		NextHop:        def.NextHop,
		InterfaceIndex: int(def.InterfaceIndex),
		Metric:         int(def.Metric),
		Present:        def.Present,
		InterfaceLUID:  def.InterfaceLUID,
	}
	w.journal = recoverylog.Journal{Phase: "capture", Applied: nil}
	if def.Present {
		m := recoverylog.Mutation{
			ID:         "orig-default",
			Kind:       "original_default",
			DestPrefix: "0.0.0.0/0",
			NextHop:    def.NextHop.String(),
			IfIndex:    def.InterfaceIndex,
			IfLUID:     def.InterfaceLUID,
			Metric:     def.Metric,
		}
		w.journal.OriginalDefault = &m
	}
	// Capture IPv6 on ALL active non-Nyxveil egress interfaces (fail-closed).
	p.CapturedIPv6 = nil
	p.CapturedIPv6Phys = nil
	p.CapturedIPv6IfIndex = 0
	states, err := winnet.CaptureAllEgressIPv6(tunAdapterName)
	if err != nil {
		return fmt.Errorf("routes: IPv6 capture: %w", err)
	}
	for _, st := range states {
		en := st.Enabled
		mut := recoverylog.Mutation{
			ID:             fmt.Sprintf("orig-ipv6-%d", st.InterfaceIndex),
			Kind:           "ipv6_set",
			IPv6IfIndex:    st.InterfaceIndex,
			IPv6WasEnabled: &en,
		}
		w.journal.OriginalIPv6 = append(w.journal.OriginalIPv6, mut)
		p.CapturedIPv6 = append(p.CapturedIPv6, IPv6Capture{InterfaceIndex: st.InterfaceIndex, Enabled: en})
		if p.CapturedIPv6Phys == nil {
			w.journal.OriginalIPv6Phys = &mut
			p.CapturedIPv6Phys = &en
			p.CapturedIPv6IfIndex = st.InterfaceIndex
		}
	}
	return w.persist()
}

func (w *WindowsApplier) ApplyBypass(p *Plan) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !p.CapturedDefault.Present || !p.CapturedDefault.NextHop.IsValid() {
		return fmt.Errorf("routes: no default gateway for bypass")
	}
	fallbackGW := p.CapturedDefault.NextHop
	fallbackLUID := p.CapturedDefault.InterfaceLUID
	fallbackIdx := uint32(p.CapturedDefault.InterfaceIndex)
	for i := range p.BypassHosts {
		h := &p.BypassHosts[i]
		gw := fallbackGW
		luid := fallbackLUID
		ifIdx := fallbackIdx
		// Prefer GetBestRoute2 for the actual pre-VPN path to this destination
		// (accounts for interface metric/policy, not just lowest 0/0 route metric).
		if dest := h.Destination.Addr(); dest.IsValid() {
			if br, err := winnet.ResolveIPv4BestRoute(dest); err == nil && br.Present && br.NextHop.IsValid() {
				gw = br.NextHop
				luid = br.InterfaceLUID
				ifIdx = br.InterfaceIndex
			}
		}
		if h.NextHop.IsValid() {
			gw = h.NextHop
		} else {
			h.NextHop = gw
		}
		dst := h.Destination
		mut := recoverylog.Mutation{
			ID:         fmt.Sprintf("bypass-%s", dst.Addr()),
			Kind:       "bypass_route",
			DestPrefix: dst.String(),
			NextHop:    gw.String(),
			IfIndex:    ifIdx,
			IfLUID:     luid,
			Metric:     1,
		}
		w.journal.Pending = &mut
		w.journal.Phase = "bypass"
		if err := w.persist(); err != nil {
			w.journal.Pending = nil
			return err
		}
		spec := winnet.RouteSpec{
			Destination:    dst,
			NextHop:        gw,
			InterfaceIndex: ifIdx,
			InterfaceLUID:  luid,
			Metric:         1,
		}
		if err := winnet.AddRoute(spec); err != nil {
			// fallback route.exe with gateway (still journaled)
			if err2 := run("route", "add", dst.Addr().String(), "mask", "255.255.255.255", gw.String(), "metric", "1"); err2 != nil {
				w.journal.Pending = nil
				_ = w.rollbackLocked(p)
				return fmt.Errorf("routes: bypass %s: %v / %v", dst, err, err2)
			}
		}
		diag.InfoFields("ROUTE", "bypass_added", map[string]string{
			"prefix":  dst.String(),
			"nexthop": gw.String(),
			"ifIndex": strconv.Itoa(int(ifIdx)),
		})
		w.journal.Pending = nil
		w.journal.Applied = append(w.journal.Applied, mut)
		if err := w.persist(); err != nil {
			if rbErr := w.rollbackLocked(p); rbErr != nil {
				return fmt.Errorf("routes: persist bypass applied: %v; rollback: %w", err, rbErr)
			}
			return fmt.Errorf("routes: persist bypass applied: %w", err)
		}
		crashAfter("bypass")
	}
	return nil
}

func (w *WindowsApplier) ApplyTunnel(p *Plan) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if p.TunName == "" || !p.TunPrefix.IsValid() {
		return fmt.Errorf("routes: tunnel plan incomplete")
	}
	ip := p.TunPrefix.Addr().String()
	mask := cidrMask(p.TunPrefix.Bits())

	mutAddr := recoverylog.Mutation{ID: "tun-addr", Kind: "tun_addr", TunName: p.TunName, DestPrefix: p.TunPrefix.String()}
	w.journal.Pending = &mutAddr
	if err := w.persist(); err != nil {
		w.journal.Pending = nil
		return err
	}
	if err := run("netsh", "interface", "ip", "set", "address", "name="+p.TunName, "static", ip, mask, "none"); err != nil {
		w.journal.Pending = nil
		_ = w.rollbackLocked(p)
		return fmt.Errorf("routes: set address: %w", err)
	}
	// netsh alone is insufficient for dataplane: Wintun may keep WeakHostSend /
	// Tentative DAD / SkipAsSource, so Windows selects the physical NIC source while
	// still forwarding via the tunnel — anti-spoof then drops every packet.
	if p.TunLUID != 0 && p.TunIfIndex != 0 {
		info, err := winnet.EnsureTunnelUnicastIPv4(p.TunLUID, p.TunIfIndex, p.TunPrefix.Addr(), p.TunPrefix.Bits())
		if err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: ensure tunnel unicast: %w", err)
		}
		diag.InfoFields("NETWORK", "unicast_ok", map[string]string{
			"addr":           info.Addr.String(),
			"prefix":         strconv.Itoa(int(info.OnLinkPrefixLength)),
			"dad":            info.DadStateName(),
			"skip_as_source": strconv.FormatBool(info.SkipAsSource),
			"ifIndex":        strconv.Itoa(int(info.InterfaceIndex)),
		})
		harden, err := winnet.HardenTunnelIPv4Interface(p.TunLUID, p.TunIfIndex, 1)
		if err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: harden tunnel interface: %w", err)
		}
		b := harden.Before
		diag.InfoFields("NETWORK", "iface_harden_before", map[string]string{
			"family":             strconv.Itoa(int(b.Family)),
			"ifIndex":            strconv.Itoa(int(b.InterfaceIndex)),
			"luid":               strconv.FormatUint(b.InterfaceLUID, 10),
			"site_prefix_length": strconv.FormatUint(uint64(b.SitePrefixLength), 10),
			"weak_host_send":     strconv.Itoa(int(b.WeakHostSend)),
			"weak_host_receive":  strconv.Itoa(int(b.WeakHostReceive)),
			"metric":             strconv.FormatUint(uint64(b.Metric), 10),
			"nl_mtu":             strconv.FormatUint(uint64(b.NlMtu), 10),
		})
		a := harden.After
		diag.InfoFields("NETWORK", "iface_hardened", map[string]string{
			"weak_host_send":     strconv.Itoa(int(a.WeakHostSend)),
			"weak_host_receive":  strconv.Itoa(int(a.WeakHostReceive)),
			"site_prefix_length": strconv.FormatUint(uint64(a.SitePrefixLength), 10),
			"metric":             strconv.FormatUint(uint64(a.Metric), 10),
			"nl_mtu":             strconv.FormatUint(uint64(a.NlMtu), 10),
			"ifIndex":            strconv.Itoa(int(a.InterfaceIndex)),
			"skipped_set":        strconv.FormatBool(harden.SkippedSet),
			"skip_reason":        harden.SkipReason,
		})
	}
	w.journal.Pending = nil
	w.journal.Applied = append(w.journal.Applied, mutAddr)
	p.Gate.AddressApplied = true
	if err := w.persist(); err != nil {
		if rbErr := w.rollbackLocked(p); rbErr != nil {
			return fmt.Errorf("routes: persist tun_addr: %v; rollback: %w", err, rbErr)
		}
		return fmt.Errorf("routes: persist tun_addr: %w", err)
	}
	crashAfter("tun_addr")

	if p.TunMTU > 0 {
		_ = run("netsh", "interface", "ipv4", "set", "subinterface", p.TunName, "mtu="+strconv.Itoa(p.TunMTU), "store=active")
	}

	if len(p.TunDNS) > 0 {
		dnsStr := make([]string, len(p.TunDNS))
		for i, d := range p.TunDNS {
			dnsStr[i] = d.String()
		}
		mutDNS := recoverylog.Mutation{ID: "tun-dns", Kind: "tun_dns", TunName: p.TunName, DNS: dnsStr}
		w.journal.Pending = &mutDNS
		if err := w.persist(); err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: persist tun_dns pending: %w", err)
		}
		_ = winnet.ClearDNSServersOnAlias(p.TunName)
		if err := winnet.SetDNSServersOnAlias(p.TunName, dnsStr); err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: set dns: %w", err)
		}
		w.journal.Pending = nil
		w.journal.Applied = append(w.journal.Applied, mutDNS)
		p.Gate.DNSApplied = true
		if err := w.persist(); err != nil {
			if rbErr := w.rollbackLocked(p); rbErr != nil {
				return fmt.Errorf("routes: persist tun_dns: %v; rollback: %w", err, rbErr)
			}
			return fmt.Errorf("routes: persist tun_dns: %w", err)
		}
		crashAfter("tun_dns")
	} else {
		return fmt.Errorf("routes: dns_servers required on tunnel")
	}

	// Physical IPv6: disable on every captured active egress IF; record exact prior state.
	ipv6Targets := p.CapturedIPv6
	if len(ipv6Targets) == 0 && p.CapturedIPv6IfIndex > 0 && p.CapturedIPv6Phys != nil {
		ipv6Targets = []IPv6Capture{{InterfaceIndex: p.CapturedIPv6IfIndex, Enabled: *p.CapturedIPv6Phys}}
	}
	for _, cap := range ipv6Targets {
		was := cap.Enabled
		now := false
		mut := recoverylog.Mutation{
			ID:             fmt.Sprintf("ipv6-phys-%d", cap.InterfaceIndex),
			Kind:           "ipv6_set",
			IPv6IfIndex:    cap.InterfaceIndex,
			IPv6WasEnabled: &was,
			IPv6NowEnabled: &now,
		}
		w.journal.Pending = &mut
		if err := w.persist(); err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: persist ipv6 pending: %w", err)
		}
		if was {
			if err := winnet.SetIPv6Enabled(cap.InterfaceIndex, false); err != nil {
				w.journal.Pending = nil
				_ = w.rollbackLocked(p)
				return fmt.Errorf("routes: ipv6 block if %d: %w", cap.InterfaceIndex, err)
			}
		}
		w.journal.Pending = nil
		w.journal.Applied = append(w.journal.Applied, mut)
		if err := w.persist(); err != nil {
			if rbErr := w.rollbackLocked(p); rbErr != nil {
				return fmt.Errorf("routes: persist ipv6: %v; rollback: %w", err, rbErr)
			}
			return fmt.Errorf("routes: persist ipv6: %w", err)
		}
		crashAfter("ipv6")
	}

	// Full-tunnel IPv4 via split defaults (0.0.0.0/1 + 128.0.0.0/1) bound to Wintun.
	// Skipped only for isolated gate transactions that must not replace the host default.
	if w.SkipDefaultVPN {
		w.journal.Phase = "tunnel"
		p.DefaultViaTUN = true
		p.Gate.RoutesApplied = true
		return w.persist()
	}
	if p.TunIfIndex == 0 || p.TunLUID == 0 || !p.TunGateway.IsValid() {
		_ = w.rollbackLocked(p)
		return fmt.Errorf("routes: tunnel ifIndex/LUID/gateway required before full-tunnel routes")
	}
	// On-link next hop (0.0.0.0) bound to Wintun — WireGuard-style split default.
	// Using TunGateway without ensuring on-link reachability is a common silent failure mode.
	onLink := netip.IPv4Unspecified()
	for _, dest := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		pfx := netip.MustParsePrefix(dest)
		mutDef := recoverylog.Mutation{
			ID:         "default-vpn-" + dest,
			Kind:       "default_vpn",
			DestPrefix: dest,
			NextHop:    onLink.String(),
			IfIndex:    p.TunIfIndex,
			IfLUID:     p.TunLUID,
			Metric:     1,
		}
		w.journal.Pending = &mutDef
		if err := w.persist(); err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: persist default_vpn pending %s: %w", dest, err)
		}
		spec := winnet.RouteSpec{
			Destination:    pfx,
			NextHop:        onLink,
			InterfaceIndex: p.TunIfIndex,
			InterfaceLUID:  p.TunLUID,
			Metric:         1,
		}
		if err := winnet.AddRoute(spec); err != nil {
			mask := cidrMask(pfx.Bits())
			if err2 := run("route", "add", pfx.Addr().String(), "mask", mask, onLink.String(), "metric", "1", "if", strconv.Itoa(int(p.TunIfIndex))); err2 != nil {
				w.journal.Pending = nil
				_ = w.rollbackLocked(p)
				diag.Error("ROUTE", "add_failed", dest+" "+err.Error())
				return fmt.Errorf("routes: full-tunnel %s via TUN: %v / %v", dest, err, err2)
			}
		}
		diag.InfoFields("ROUTE", "added", map[string]string{
			"prefix":  dest,
			"ifIndex": strconv.Itoa(int(p.TunIfIndex)),
			"luid":    fmt.Sprintf("%d", p.TunLUID),
			"nexthop": onLink.String(),
		})
		w.journal.Pending = nil
		w.journal.Applied = append(w.journal.Applied, mutDef)
		if err := w.persist(); err != nil {
			if rbErr := w.rollbackLocked(p); rbErr != nil {
				return fmt.Errorf("routes: persist default_vpn %s: %v; rollback: %w", dest, err, rbErr)
			}
			return fmt.Errorf("routes: persist default_vpn %s: %w", dest, err)
		}
		crashAfter("default_vpn_" + dest)
	}
	p.DefaultViaTUN = true
	p.Gate.RoutesApplied = true
	w.journal.Phase = "tunnel"
	return w.persist()
}

// VerifyTunnel confirms the Wintun adapter, address, split default routes, and DNS
// are present on the OS. Connected must not be set if this fails.
func (w *WindowsApplier) VerifyTunnel(p *Plan) error {
	if p == nil || p.TunName == "" {
		return fmt.Errorf("routes: verify: empty tunnel plan")
	}
	idx, err := winnet.InterfaceIndexByAlias(p.TunName)
	if err != nil || idx == 0 {
		return fmt.Errorf("routes: verify: Wintun adapter %q missing: %w", p.TunName, err)
	}
	if p.TunIfIndex != 0 && idx != p.TunIfIndex {
		return fmt.Errorf("routes: verify: adapter ifIndex changed %d→%d", p.TunIfIndex, idx)
	}
	p.Gate.AdapterOpen = true

	if !p.Gate.AddressApplied || !p.TunPrefix.IsValid() {
		return fmt.Errorf("routes: verify: tunnel address not applied")
	}
	wantIP := p.TunPrefix.Addr().Unmap()
	if p.TunLUID != 0 {
		info, found, err := winnet.FindUnicastIPv4OnLUID(p.TunLUID, wantIP)
		if err != nil {
			return fmt.Errorf("routes: verify: lookup unicast: %w", err)
		}
		if !found {
			return fmt.Errorf("routes: verify: VPN IP %s missing on Wintun luid=%d", wantIP, p.TunLUID)
		}
		if info.DadState != 4 /* IpDadStatePreferred */ {
			return fmt.Errorf("routes: verify: VPN IP %s DadState=%s want Preferred", wantIP, info.DadStateName())
		}
		if info.SkipAsSource {
			return fmt.Errorf("routes: verify: VPN IP %s has SkipAsSource=true", wantIP)
		}
		if info.ValidLifetime == 0 || info.PreferredLifetime == 0 {
			return fmt.Errorf("routes: verify: VPN IP %s lifetime valid=%d preferred=%d",
				wantIP, info.ValidLifetime, info.PreferredLifetime)
		}
		diag.InfoFields("NETWORK", "verify_unicast", map[string]string{
			"addr":           info.Addr.String(),
			"dad":            info.DadStateName(),
			"skip_as_source": "false",
			"prefix":         strconv.Itoa(int(info.OnLinkPrefixLength)),
		})
	}
	if !w.SkipDefaultVPN {
		// Fail-closed: Windows must select the current TypeConfig VPN IP as BestSource
		// for a new public IPv4 flow (TEST-NET-1 / documentation range).
		probe := netip.AddrFrom4([4]byte{192, 0, 2, 1})
		br, err := winnet.ResolveIPv4BestRoute(probe)
		if err != nil {
			return fmt.Errorf("routes: verify: GetBestRoute2: %w", err)
		}
		if br.InterfaceLUID != p.TunLUID || br.InterfaceIndex != idx {
			return fmt.Errorf("routes: verify: BestRoute if LUID/Index %d/%d want tunnel %d/%d",
				br.InterfaceLUID, br.InterfaceIndex, p.TunLUID, idx)
		}
		if !br.BestSource.IsValid() || br.BestSource.Unmap() != wantIP {
			return fmt.Errorf("routes: verify: BestSource=%v want VPN IP %s (Windows source selection broken)",
				br.BestSource, wantIP)
		}
		diag.InfoFields("NETWORK", "verify_best_source", map[string]string{
			"dest":         probe.String(),
			"best_source":  br.BestSource.String(),
			"ifIndex":      strconv.Itoa(int(br.InterfaceIndex)),
			"expected_src": wantIP.String(),
		})
	}
	if !w.SkipDefaultVPN {
		if !p.Gate.RoutesApplied || !p.DefaultViaTUN {
			return fmt.Errorf("routes: verify: full-tunnel routes not applied")
		}
		onLink := netip.IPv4Unspecified()
		for _, dest := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
			pfx := netip.MustParsePrefix(dest)
			ok, err := winnet.HasIPv4Route(winnet.RouteSpec{
				Destination:    pfx,
				NextHop:        onLink,
				InterfaceIndex: idx,
				InterfaceLUID:  p.TunLUID,
				Metric:         1,
			})
			if err != nil {
				return fmt.Errorf("routes: verify: lookup %s: %w", dest, err)
			}
			if !ok {
				return fmt.Errorf("routes: verify: missing full-tunnel route %s on-link if=%d luid=%d", dest, idx, p.TunLUID)
			}
			diag.InfoFields("ROUTE", "verify_ok", map[string]string{
				"prefix": dest, "ifIndex": strconv.Itoa(int(idx)),
			})
		}
	} else {
		p.Gate.RoutesApplied = true
	}
	if len(p.TunDNS) == 0 || !p.Gate.DNSApplied {
		return fmt.Errorf("routes: verify: DNS not applied to tunnel")
	}
	servers, err := winnet.DNSServersOnInterface(idx)
	if err != nil {
		return fmt.Errorf("routes: verify: read DNS: %w", err)
	}
	want := p.TunDNS[0].String()
	found := false
	for _, s := range servers {
		if s == want {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("routes: verify: DNS %s not on adapter %q (have %v)", want, p.TunName, servers)
	}
	return nil
}

func (w *WindowsApplier) Restore(p *Plan) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rollbackLocked(p)
}

func (w *WindowsApplier) rollbackLocked(p *Plan) error {
	var first error
	remaining := make([]recoverylog.Mutation, 0)
	// Undo pending first if crash mid-step.
	if w.journal.Pending != nil {
		if err := undoMutation(*w.journal.Pending); err != nil {
			if first == nil {
				first = err
			}
			remaining = append(remaining, *w.journal.Pending)
		}
		w.journal.Pending = nil
	}
	for i := len(w.journal.Applied) - 1; i >= 0; i-- {
		m := w.journal.Applied[i]
		if err := undoMutation(m); err != nil {
			if first == nil {
				first = err
			}
			// Keep unreverted mutations (preserve reverse-apply order on retry).
			remaining = append([]recoverylog.Mutation{m}, remaining...)
			continue
		}
	}
	if p != nil && first == nil {
		p.DefaultViaTUN = false
	}
	if first != nil {
		// Preserve durable journal with still-unreverted mutations — never destroy last recovery info.
		w.journal.Applied = remaining
		w.journal.Phase = "restore_failed"
		_ = w.persist()
		return first
	}
	if err := recoverylog.Clear(w.JournalPath); err != nil {
		w.journal.Applied = remaining
		w.journal.Phase = "restore_failed"
		_ = w.persist()
		return err
	}
	w.journal = recoverylog.Journal{}
	return nil
}

func undoMutation(m recoverylog.Mutation) error {
	switch m.Kind {
	case "bypass_route", "default_vpn":
		pfx, err := netip.ParsePrefix(m.DestPrefix)
		if err != nil {
			return err
		}
		nh, err := netip.ParseAddr(m.NextHop)
		if err != nil || m.NextHop == "" {
			return fmt.Errorf("routes: undo %s missing next hop", m.Kind)
		}
		spec := winnet.RouteSpec{
			Destination:    pfx,
			NextHop:        nh,
			InterfaceIndex: m.IfIndex,
			InterfaceLUID:  m.IfLUID,
			Metric:         m.Metric,
		}
		if err := winnet.DeleteRoute(spec); err != nil {
			mask := cidrMask(pfx.Bits())
			args := []string{"delete", pfx.Addr().String(), "mask", mask, nh.String()}
			if m.IfIndex != 0 {
				args = append(args, "if", strconv.FormatUint(uint64(m.IfIndex), 10))
			}
			if err2 := run("route", args...); err2 != nil {
				// Already absent is clean for recovery (adapter/route torn down).
				msg := strings.ToLower(err2.Error())
				if strings.Contains(msg, "not found") || strings.Contains(msg, "element not found") ||
					strings.Contains(msg, "cannot find") || strings.Contains(msg, "the route deletion") {
					return nil
				}
				return err2
			}
		}
		return nil
	case "tun_dns":
		return winnet.ClearDNSServersOnAlias(m.TunName)
	case "tun_addr":
		if m.TunName != "" {
			ok, err := winnet.AdapterExistsByAlias(m.TunName)
			if err == nil && !ok {
				return nil // adapter already gone
			}
		}
		if err := run("netsh", "interface", "ip", "set", "address", "name="+m.TunName, "dhcp"); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "no such") || strings.Contains(msg, "not found") ||
				strings.Contains(msg, "file not found") || strings.Contains(msg, "element not found") {
				return nil
			}
			return err
		}
		return nil
	case "ipv6_set":
		if m.IPv6WasEnabled == nil || m.IPv6IfIndex == 0 {
			return nil
		}
		if err := winnet.SetIPv6Enabled(m.IPv6IfIndex, *m.IPv6WasEnabled); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "no adapter") || strings.Contains(msg, "not found") ||
				strings.Contains(msg, "objectnotfound") {
				return nil
			}
			return err
		}
		return nil
	default:
		return nil
	}
}

func (w *WindowsApplier) RecoverOnStartup() error {
	j, err := recoverylog.Read(w.JournalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// Fail-closed: unreadable journal must not allow service readiness.
		return fmt.Errorf("routes: read recovery journal: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.journal = *j
	if err := w.rollbackLocked(&Plan{}); err != nil {
		return fmt.Errorf("routes: recovery refuse ready (dirty journal): %w", err)
	}
	// Belt-and-suspenders: journal must be gone after successful restore.
	if _, statErr := os.Stat(w.JournalPath); statErr == nil {
		return fmt.Errorf("routes: recovery refuse ready: journal still present after restore")
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cidrMask(bits int) string {
	var mask uint32
	if bits > 0 {
		mask = ^uint32(0) << uint(32-bits)
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(mask>>24), byte(mask>>16), byte(mask>>8), byte(mask))
}

func ResolveHostIPs(host string) []netip.Addr {
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is4() {
		return []netip.Addr{ip}
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			a, ok := netip.AddrFromSlice(v4)
			if ok {
				out = append(out, a)
			}
		}
	}
	return out
}
