package mtu

import (
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Exact NVP wire framing for DATA over QUIC DATAGRAM (copied from Windows engine lessons).
const (
	nvpWireLenPrefix     = 4
	nvpEpochBytes        = 4
	nvpSequenceBytes     = 8
	nvpInnerHeader       = 4
	nvpAEADTagBytes      = chacha20poly1305.Overhead
	NVPFixedWireOverhead = nvpWireLenPrefix + nvpEpochBytes + nvpSequenceBytes + nvpInnerHeader + nvpAEADTagBytes // 36
	NVPDefaultMaxPadding = 64
	HTTP3DatagramPrefixMax = 8
)

func NVPOverheadWorstCase() int {
	return NVPFixedWireOverhead + NVPDefaultMaxPadding
}

// EffectiveTunnelMTU clamps TypeConfig MTU to the QUIC DATAGRAM budget.
func EffectiveTunnelMTU(typeConfigMTU, maxDatagramPayload int) (int, error) {
	if typeConfigMTU <= 0 {
		return 0, fmt.Errorf("mtu: invalid typeconfig mtu %d", typeConfigMTU)
	}
	if maxDatagramPayload <= 0 {
		// No datagrams — conservative TLS path.
		if typeConfigMTU > 1280 {
			return 1280, nil
		}
		return typeConfigMTU, nil
	}
	maxNVP := maxDatagramPayload - HTTP3DatagramPrefixMax
	maxIP := maxNVP - NVPFixedWireOverhead - NVPDefaultMaxPadding
	if maxIP < 576 {
		return 0, fmt.Errorf("mtu: datagram budget too small (%d)", maxIP)
	}
	if typeConfigMTU < maxIP {
		return typeConfigMTU, nil
	}
	return maxIP, nil
}
