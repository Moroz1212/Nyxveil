package engine

import (
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Exact NVP wire framing for DATA over QUIC DATAGRAM (Frozen Core EncodeWireRecord + AEAD).
//
// EncodeInner:  msgType(1)+flags(1)+padLen(2) + payload + padding  = 4 + ip + pad
// Seal:         ciphertext = inner + Poly1305 tag (16)
// EncodeWireRecord (sent as DATAGRAM payload before HTTP/3 quarter-stream-id prefix):
//   length(4) + epoch(4) + sequence(8) + ciphertext
//
// wire_len = 4+4+8 + (4+ip+pad) + 16 = 36 + ip + pad
const (
	nvpWireLenPrefix   = 4
	nvpEpochBytes      = 4
	nvpSequenceBytes   = 8
	nvpInnerHeader     = 4
	nvpAEADTagBytes    = chacha20poly1305.Overhead // 16
	NVPFixedWireOverhead = nvpWireLenPrefix + nvpEpochBytes + nvpSequenceBytes + nvpInnerHeader + nvpAEADTagBytes // 36

	// DefaultPaddingPolicy MaxBytes in Frozen Core session.DefaultPaddingPolicy.
	NVPDefaultMaxPadding = 64

	// HTTP/3 DATAGRAM (RFC 9297) prepends quarter-stream-id varint before NVP wire
	// (quic-go http3.connection.sendDatagram). Max varint length is 8.
	HTTP3DatagramPrefixMax = 8

)

// NVPWireLen returns the EncodeWireRecord size for an IP packet with given padding.
func NVPWireLen(ipPacketLen, paddingLen int) int {
	if ipPacketLen < 0 || paddingLen < 0 {
		return -1
	}
	return NVPFixedWireOverhead + ipPacketLen + paddingLen
}

// NVPOverheadWorstCase is fixed framing + max default padding (no HTTP/3 prefix).
func NVPOverheadWorstCase() int {
	return NVPFixedWireOverhead + NVPDefaultMaxPadding
}

// DatagramBudget describes how a QUIC MaxDatagramPayloadSize maps to tunnel MTU.
type DatagramBudget struct {
	MaxDatagramPayload int // quic.DatagramTooLargeError.MaxDatagramPayloadSize
	HTTP3PrefixMax     int // conservative quarter-stream-id allowance
	NVPFixedOverhead   int
	NVPMaxPadding      int
	MaxNVPWire         int
	MaxIPPacket        int
}

// ComputeDatagramBudget derives max IP packet size from a quic-go DATAGRAM ceiling.
// maxDatagramPayload is the full QUIC DATAGRAM payload including HTTP/3 quarter-stream-id.
func ComputeDatagramBudget(maxDatagramPayload, http3PrefixMax, maxPadding int) (DatagramBudget, error) {
	if maxDatagramPayload <= 0 {
		return DatagramBudget{}, fmt.Errorf("engine: invalid MaxDatagramPayloadSize %d", maxDatagramPayload)
	}
	if http3PrefixMax < 0 {
		http3PrefixMax = 0
	}
	if http3PrefixMax > HTTP3DatagramPrefixMax {
		http3PrefixMax = HTTP3DatagramPrefixMax
	}
	if maxPadding < 0 {
		maxPadding = 0
	}
	maxNVP := maxDatagramPayload - http3PrefixMax
	maxIP := maxNVP - NVPFixedWireOverhead - maxPadding
	return DatagramBudget{
		MaxDatagramPayload: maxDatagramPayload,
		HTTP3PrefixMax:     http3PrefixMax,
		NVPFixedOverhead:   NVPFixedWireOverhead,
		NVPMaxPadding:      maxPadding,
		MaxNVPWire:         maxNVP,
		MaxIPPacket:        maxIP,
	}, nil
}

// EffectiveTunnelMTU = min(TypeConfig.MTU, max IP that fits after NVP framing in DATAGRAM).
//
//	max_ip = MaxDatagramPayloadSize - HTTP3DatagramPrefixMax - NVPFixedWireOverhead - NVPDefaultMaxPadding
//	effective = min(TypeConfig.MTU, max_ip)
func EffectiveTunnelMTU(typeConfigMTU, maxDatagramPayload int) (effective int, budget DatagramBudget, err error) {
	budget, err = ComputeDatagramBudget(maxDatagramPayload, HTTP3DatagramPrefixMax, NVPDefaultMaxPadding)
	if err != nil {
		return 0, budget, err
	}
	if budget.MaxIPPacket <= 0 {
		return 0, budget, fmt.Errorf("engine: DATAGRAM ceiling %d too small for NVP framing (overhead=%d pad=%d http3=%d)",
			maxDatagramPayload, NVPFixedWireOverhead, NVPDefaultMaxPadding, HTTP3DatagramPrefixMax)
	}
	effective = budget.MaxIPPacket
	if typeConfigMTU > 0 && typeConfigMTU < effective {
		effective = typeConfigMTU
	}
	return effective, budget, nil
}
