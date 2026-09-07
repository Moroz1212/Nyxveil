package main

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/nyxveil/client-windows/internal/engine"
	"github.com/nyxveil/client-windows/internal/recoverylog"
	"github.com/nyxveil/client-windows/internal/winnet"
	"github.com/nyxveil/client-windows/internal/wintundev"
	"github.com/nyxveil/nvp/core/tunnel"
)

func main() {
	vpnIP := netip.MustParseAddr("46.8.218.27")
	cpHost := "cp.nyxveil.ru"
	before, _ := winnet.GetIPv4DefaultRoute()
	fmt.Printf("BEFORE def if=%d luid=%d nh=%v metric=%d\n", before.InterfaceIndex, before.InterfaceLUID, before.NextHop, before.Metric)
	br0, err := winnet.ResolveIPv4BestRoute(vpnIP)
	fmt.Printf("BEFORE best vpnIP present=%v nh=%v if=%d luid=%d metric=%d err=%v\n", br0.Present, br0.NextHop, br0.InterfaceIndex, br0.InterfaceLUID, br0.Metric, err)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dev, err := wintundev.Open(ctx, tunnel.Config{Name: "Nyxveil", MTU: 1280})
	if err != nil {
		fmt.Printf("OPEN_ERR %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = dev.Close() }()
	luid, _ := wintundev.AdapterLUID(dev)
	ifIdx, _ := winnet.InterfaceIndexByAlias("Nyxveil")

	applier := engine.NewWindowsApplier()
	applier.JournalPath = recoverylog.DefaultPath() + ".bypassdebug"
	_ = os.Remove(applier.JournalPath)
	plan := engine.NewPlan()
	if err := applier.Capture(plan); err != nil {
		fmt.Printf("CAPTURE_ERR %v\n", err)
		os.Exit(1)
	}
		hosts := []engine.HostRoute{{Destination: netip.PrefixFrom(vpnIP, 32)}}
	for _, ip := range engine.ResolveHostIPs(cpHost) {
		hosts = append(hosts, engine.HostRoute{Destination: netip.PrefixFrom(ip, 32)})
	}
	plan.SetBypassHosts(hosts)
	fmt.Printf("BYPASS count=%d\n", len(hosts))
	for _, h := range hosts {
		fmt.Printf("  host %s\n", h.Destination)
	}
	if err := applier.ApplyBypass(plan); err != nil {
		fmt.Printf("BYPASS_ERR %v\n", err)
		os.Exit(1)
	}
	brB, _ := winnet.ResolveIPv4BestRoute(vpnIP)
	fmt.Printf("AFTER_BYPASS best vpnIP nh=%v if=%d luid=%d metric=%d\n", brB.NextHop, brB.InterfaceIndex, brB.InterfaceLUID, brB.Metric)

	_ = plan.ApplyTypeConfig("Nyxveil", "10.77.0.2", 24, "10.77.0.1", []string{"10.77.0.1"}, 1280)
	plan.TunIfIndex = ifIdx
	plan.TunLUID = luid
	plan.Gate.AdapterOpen = true
	plan.Gate.TypeConfigOK = true
	if err := applier.ApplyTunnel(plan); err != nil {
		fmt.Printf("APPLY_ERR %v\n", err)
		_ = applier.Restore(plan)
		os.Exit(1)
	}
	defer func() {
		_ = applier.Restore(plan)
		_ = os.Remove(applier.JournalPath)
	}()

	for _, wait := range []time.Duration{0, time.Second, 2 * time.Second, 5 * time.Second} {
		if wait > 0 {
			time.Sleep(wait)
		}
		br, err := winnet.ResolveIPv4BestRoute(vpnIP)
		viaTUN := br.InterfaceLUID == luid
		fmt.Printf("T+%v best vpnIP nh=%v if=%d luid=%d metric=%d viaTUN=%v err=%v\n", wait, br.NextHop, br.InterfaceIndex, br.InterfaceLUID, br.Metric, viaTUN, err)
		ok, err := winnet.HasIPv4Route(winnet.RouteSpec{
			Destination:    netip.PrefixFrom(vpnIP, 32),
			NextHop:        plan.CapturedDefault.NextHop,
			InterfaceIndex: uint32(plan.CapturedDefault.InterfaceIndex),
			InterfaceLUID:  plan.CapturedDefault.InterfaceLUID,
			Metric:         1,
		})
		fmt.Printf("T+%v HasBypassRoute ok=%v err=%v (expect nh=%v if=%d)\n", wait, ok, err, plan.CapturedDefault.NextHop, plan.CapturedDefault.InterfaceIndex)
		if viaTUN {
			fmt.Printf("T+%v FATAL: VPN endpoint routed into TUN (blackhole)\n", wait)
		}
	}
}

