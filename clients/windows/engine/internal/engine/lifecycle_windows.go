//go:build windows

package engine

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nyxveil/client-windows/internal/winnet"
)

// LifecycleMonitor watches sleep/resume and default-route identity changes.
// On stale physical gateway/adapter it disconnects safely (no zombie routes).
type LifecycleMonitor struct {
	mu       sync.Mutex
	mgr      *Manager
	stop     chan struct{}
	wg       sync.WaitGroup
	lastLUID uint64
	lastIdx  uint32
}

func NewLifecycleMonitor(mgr *Manager) *LifecycleMonitor {
	return &LifecycleMonitor{mgr: mgr, stop: make(chan struct{})}
}

func (m *LifecycleMonitor) Start() {
	m.wg.Add(1)
	go m.loop()
}

func (m *LifecycleMonitor) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	m.wg.Wait()
}

// OnPowerResume is invoked after Windows resume; tears down stale tunnels.
func (m *LifecycleMonitor) OnPowerResume() {
	st, _ := m.mgr.State.Get()
	if st.String() == "Disconnected" || st.String() == "Disconnecting" {
		return
	}
	log.Printf("lifecycle: power resume — safe disconnect (stale transport risk)")
	_ = m.mgr.Disconnect(context.Background())
}

func (m *LifecycleMonitor) loop() {
	defer m.wg.Done()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	m.snapshotRoute()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.checkRouteDrift()
			m.checkIPv6EgressDrift()
		}
	}
}

func (m *LifecycleMonitor) snapshotRoute() {
	def, err := winnet.GetIPv4DefaultRoute()
	if err != nil || !def.Present {
		return
	}
	m.mu.Lock()
	m.lastLUID = def.InterfaceLUID
	m.lastIdx = def.InterfaceIndex
	m.mu.Unlock()
}

func (m *LifecycleMonitor) checkRouteDrift() {
	st, _ := m.mgr.State.Get()
	if st.String() != "Connected" && st.String() != "Reconnecting" {
		m.snapshotRoute()
		return
	}
	def, err := winnet.GetIPv4DefaultRoute()
	if err != nil || !def.Present {
		return
	}
	m.mu.Lock()
	prevLUID, prevIdx := m.lastLUID, m.lastIdx
	m.mu.Unlock()
	if prevLUID == 0 && prevIdx == 0 {
		m.snapshotRoute()
		return
	}
	// While VPN is up, physical default may be replaced by TUN — capture is approximate.
	// Detect Ethernet↔Wi-Fi by InterfaceLUID change of the *original* physical route is
	// handled via journal OriginalDefault; here we detect unexpected physical LUID flip
	// when the best non-TUN default changes after resume/network switch.
	if def.InterfaceLUID != 0 && prevLUID != 0 && def.InterfaceLUID != prevLUID {
		// If metric suggests our VPN default (metric 1 via TUN), ignore.
		if def.Metric <= 1 {
			return
		}
		log.Printf("lifecycle: default route LUID changed %d→%d — safe disconnect", prevLUID, def.InterfaceLUID)
		_ = m.mgr.Disconnect(context.Background())
		m.snapshotRoute()
	}
}

// checkIPv6EgressDrift disconnects if a new active non-Nyxveil egress IF appears
// that was not captured/protected at Connect time (fail-closed vs IPv6 leak).
func (m *LifecycleMonitor) checkIPv6EgressDrift() {
	st, _ := m.mgr.State.Get()
	if st.String() != "Connected" {
		return
	}
	m.mgr.mu.Lock()
	known := map[uint32]struct{}{}
	if m.mgr.plan != nil {
		for _, c := range m.mgr.plan.CapturedIPv6 {
			known[c.InterfaceIndex] = struct{}{}
		}
	}
	m.mgr.mu.Unlock()

	idxs, err := winnet.ListActiveEgressIfIndexes(tunAdapterName)
	if err != nil {
		log.Printf("lifecycle: IPv6 egress enum failed — safe disconnect: %v", err)
		_ = m.mgr.Disconnect(context.Background())
		return
	}
	for _, idx := range idxs {
		if _, ok := known[idx]; ok {
			continue
		}
		// New adapter: verify we can still evaluate IPv6; either way disconnect.
		if _, err := winnet.CaptureIPv6State(idx); err != nil {
			log.Printf("lifecycle: new egress if %d IPv6 unevaluable — disconnect: %v", idx, err)
		} else {
			log.Printf("lifecycle: new egress if %d appeared while Connected — safe disconnect", idx)
		}
		_ = m.mgr.Disconnect(context.Background())
		return
	}
}
