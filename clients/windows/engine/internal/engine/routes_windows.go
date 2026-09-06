//go:build windows

package engine

import (
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"

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
	w.journal.Pending = nil
	w.journal.Applied = append(w.journal.Applied, mutAddr)
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
		_ = run("netsh", "interface", "ip", "delete", "dns", "name="+p.TunName, "all")
		if err := run("netsh", "interface", "ip", "set", "dns", "name="+p.TunName, "static", dnsStr[0]); err != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: set dns: %w", err)
		}
		for i := 1; i < len(dnsStr); i++ {
			_ = run("netsh", "interface", "ip", "add", "dns", "name="+p.TunName, dnsStr[i], "index="+strconv.Itoa(i+1))
		}
		w.journal.Pending = nil
		w.journal.Applied = append(w.journal.Applied, mutDNS)
		if err := w.persist(); err != nil {
			if rbErr := w.rollbackLocked(p); rbErr != nil {
				return fmt.Errorf("routes: persist tun_dns: %v; rollback: %w", err, rbErr)
			}
			return fmt.Errorf("routes: persist tun_dns: %w", err)
		}
		crashAfter("tun_dns")
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

	// Default via VPN (skipped for isolated gate transactions).
	if w.SkipDefaultVPN {
		w.journal.Phase = "tunnel"
		return w.persist()
	}
	mutDef := recoverylog.Mutation{
		ID:         "default-vpn",
		Kind:       "default_vpn",
		DestPrefix: "0.0.0.0/0",
		NextHop:    p.TunGateway.String(),
		Metric:     1,
	}
	w.journal.Pending = &mutDef
	if err := w.persist(); err != nil {
		w.journal.Pending = nil
		_ = w.rollbackLocked(p)
		return fmt.Errorf("routes: persist default_vpn pending: %w", err)
	}
	pfx := netip.MustParsePrefix("0.0.0.0/0")
	spec := winnet.RouteSpec{Destination: pfx, NextHop: p.TunGateway, Metric: 1}
	if err := winnet.AddRoute(spec); err != nil {
		if err2 := run("route", "add", "0.0.0.0", "mask", "0.0.0.0", p.TunGateway.String(), "metric", "1"); err2 != nil {
			w.journal.Pending = nil
			_ = w.rollbackLocked(p)
			return fmt.Errorf("routes: default via TUN: %v / %v", err, err2)
		}
	}
	w.journal.Pending = nil
	w.journal.Applied = append(w.journal.Applied, mutDef)
	p.DefaultViaTUN = true
	w.journal.Phase = "tunnel"
	if err := w.persist(); err != nil {
		if rbErr := w.rollbackLocked(p); rbErr != nil {
			return fmt.Errorf("routes: persist default_vpn: %v; rollback: %w", err, rbErr)
		}
		return fmt.Errorf("routes: persist default_vpn: %w", err)
	}
	crashAfter("default_vpn")
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
			args := []string{"delete", pfx.Addr().String()}
			if pfx.Bits() == 0 {
				args = []string{"delete", "0.0.0.0", "mask", "0.0.0.0", nh.String()}
			} else {
				args = append(args, "mask", "255.255.255.255", nh.String())
			}
			return run("route", args...)
		}
		return nil
	case "tun_dns":
		return run("netsh", "interface", "ip", "delete", "dns", "name="+m.TunName, "all")
	case "tun_addr":
		return run("netsh", "interface", "ip", "set", "address", "name="+m.TunName, "dhcp")
	case "ipv6_set":
		if m.IPv6WasEnabled == nil || m.IPv6IfIndex == 0 {
			return nil
		}
		return winnet.SetIPv6Enabled(m.IPv6IfIndex, *m.IPv6WasEnabled)
	default:
		return nil
	}
}

func (w *WindowsApplier) RecoverOnStartup() error {
	j, err := recoverylog.Read(w.JournalPath)
	if err != nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.journal = *j
	return w.rollbackLocked(&Plan{})
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
