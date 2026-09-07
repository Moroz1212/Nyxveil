package engine

import (
	"fmt"
	"sync/atomic"

	"github.com/nyxveil/client-windows/internal/diag"
	"github.com/quic-go/quic-go"
)

// tunnelMTU tracks TypeConfig MTU vs transport-safe effective tunnel MTU.
type tunnelMTU struct {
	typeConfigMTU int
	tunName       string
	effective     atomic.Int64
	applyMTU      func(name string, mtu int) error
	injectICMP    func(pkt []byte) // optional Wintun inject
}

func newTunnelMTU(typeConfigMTU, effective int, tunName string, apply func(string, int) error) *tunnelMTU {
	t := &tunnelMTU{
		typeConfigMTU: typeConfigMTU,
		tunName:       tunName,
		applyMTU:      apply,
	}
	if effective <= 0 {
		effective = typeConfigMTU
	}
	t.effective.Store(int64(effective))
	return t
}

func (t *tunnelMTU) Effective() int {
	if t == nil {
		return 0
	}
	return int(t.effective.Load())
}

func (t *tunnelMTU) TypeConfig() int {
	if t == nil {
		return 0
	}
	return t.typeConfigMTU
}

// HandleDatagramTooLarge shrinks effective MTU when needed, notifies the stack, and
// signals that the TX pump should drop this packet and continue (recoverable).
func (t *tunnelMTU) HandleDatagramTooLarge(ipPkt []byte, dtl *quic.DatagramTooLargeError) {
	if t == nil || dtl == nil {
		return
	}
	old := t.Effective()
	newEff, budget, err := EffectiveTunnelMTU(t.typeConfigMTU, int(dtl.MaxDatagramPayloadSize))
	if err != nil {
		diag.Warn("PUMP", "tx_datagram_too_large", fmt.Sprintf("recalc failed: %v", err))
		return
	}
	meta := parseTunnelPacketMeta(ipPkt)
	encodedWorst := NVPWireLen(len(ipPkt), NVPDefaultMaxPadding)
	if encodedWorst > 0 {
		encodedWorst += HTTP3DatagramPrefixMax
	}
	fields := map[string]string{
		"packet_len":           fmt.Sprintf("%d", len(ipPkt)),
		"original_ip_len":      fmt.Sprintf("%d", len(ipPkt)),
		"encoded_len":          fmt.Sprintf("%d", encodedWorst),
		"encoded_datagram_len": fmt.Sprintf("%d", encodedWorst),
		"max_payload":          fmt.Sprintf("%d", dtl.MaxDatagramPayloadSize),
		"nvp_overhead":         fmt.Sprintf("%d", NVPOverheadWorstCase()),
		"nvp_fixed_overhead":   fmt.Sprintf("%d", NVPFixedWireOverhead),
		"http3_prefix_max":     fmt.Sprintf("%d", HTTP3DatagramPrefixMax),
		"typeconfig_mtu":       fmt.Sprintf("%d", t.typeConfigMTU),
		"old_effective_mtu":    fmt.Sprintf("%d", old),
		"new_effective_mtu":    fmt.Sprintf("%d", newEff),
		"dst":                  meta.Dst.String(),
		"protocol":             fmt.Sprintf("%d", meta.Protocol),
		"max_ip_for_ceiling":   fmt.Sprintf("%d", budget.MaxIPPacket),
	}
	diag.InfoFields("PUMP", "tx_datagram_too_large", fields)

	if newEff > 0 && newEff < old {
		t.effective.Store(int64(newEff))
		if t.applyMTU != nil && t.tunName != "" {
			if err := t.applyMTU(t.tunName, newEff); err != nil {
				diag.Warn("NETWORK", "set_effective_mtu_failed", err.Error())
			} else {
				diag.InfoFields("NETWORK", "effective_mtu_applied", map[string]string{
					"mtu":  fmt.Sprintf("%d", newEff),
					"name": t.tunName,
				})
			}
		}
	}

	if t.injectICMP != nil && newEff > 0 {
		if icmp := buildICMPv4FragNeeded(ipPkt, uint16(newEff)); len(icmp) > 0 {
			t.injectICMP(icmp)
		}
	}
}
