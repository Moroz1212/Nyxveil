package tunnel

import "github.com/nyxveil/nvp/core/transport"

// MTUConfig holds tunnel MTU configuration.
type MTUConfig struct {
	TunnelMTU        int
	TransportProfile transport.Profile
}

// Overhead estimates protocol overhead by transport profile.
func Overhead(profile transport.Profile) int {
	switch profile {
	case transport.ProfileQUICUDP:
		// QUIC + TLS 1.3 + NVP AEAD + inner framing
		return 80
	case transport.ProfileTLSTCP:
		return 70
	default:
		return 80
	}
}

// EffectiveMTU returns safe tunnel MTU accounting for transport overhead.
func EffectiveMTU(cfg MTUConfig) int {
	base := cfg.TunnelMTU
	if base <= 0 {
		base = 1280
	}
	oh := Overhead(cfg.TransportProfile)
	if base-oh < 576 {
		return 576
	}
	return base - oh
}

// SafeDefaultMTU returns conservative default for mobile networks.
func SafeDefaultMTU(profile transport.Profile) int {
	return EffectiveMTU(MTUConfig{TunnelMTU: 1280, TransportProfile: profile})
}
