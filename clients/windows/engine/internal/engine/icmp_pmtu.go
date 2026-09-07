package engine

import (
	"encoding/binary"
)

// buildICMPv4FragNeeded builds IPv4 ICMP Destination Unreachable / Fragmentation Needed
// (type 3 code 4) so the Windows stack can lower path MTU for the original flow.
// nextHopMTU is the new effective tunnel MTU. Returns a full IPv4 packet for Wintun inject.
func buildICMPv4FragNeeded(origIP []byte, nextHopMTU uint16) []byte {
	if len(origIP) < 20 || origIP[0]>>4 != 4 {
		return nil
	}
	ihl := int(origIP[0]&0x0f) * 4
	if ihl < 20 || len(origIP) < ihl {
		return nil
	}
	// ICMP payload: original IP header + first 8 bytes of original payload (RFC 792).
	copyLen := ihl + 8
	if copyLen > len(origIP) {
		copyLen = len(origIP)
	}
	icmpLen := 8 + copyLen
	total := 20 + icmpLen
	out := make([]byte, total)
	// IPv4 header
	out[0] = 0x45
	binary.BigEndian.PutUint16(out[2:4], uint16(total))
	out[8] = 64 // TTL
	out[9] = 1  // ICMP
	// src = original dst (as if from the path), dst = original src
	copy(out[12:16], origIP[16:20])
	copy(out[16:20], origIP[12:16])
	setIPv4Checksum(out[:20])

	icmp := out[20:]
	icmp[0] = 3 // Destination Unreachable
	icmp[1] = 4 // Fragmentation Needed
	binary.BigEndian.PutUint16(icmp[6:8], nextHopMTU)
	copy(icmp[8:], origIP[:copyLen])
	setICMPChecksum(icmp)
	return out
}

func setIPv4Checksum(hdr []byte) {
	binary.BigEndian.PutUint16(hdr[10:12], 0)
	var sum uint32
	for i := 0; i+1 < len(hdr); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(hdr[10:12], ^uint16(sum))
}

func setICMPChecksum(icmp []byte) {
	binary.BigEndian.PutUint16(icmp[2:4], 0)
	var sum uint32
	for i := 0; i+1 < len(icmp); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(icmp[i : i+2]))
	}
	if len(icmp)%2 == 1 {
		sum += uint32(icmp[len(icmp)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(icmp[2:4], ^uint16(sum))
}
