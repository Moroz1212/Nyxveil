package session

import (
	"crypto/rand"
	"fmt"

	"github.com/nyxveil/nvp/core/packet"
	"github.com/nyxveil/nvp/core/protocol"
	"golang.org/x/crypto/chacha20poly1305"
)

// Padding strategy names.
const (
	PaddingDisabled    = "disabled"
	PaddingRandomRange = "random-range"
	PaddingBucketed    = "bucketed"
)

// PaddingPolicy controls optional authenticated padding.
type PaddingPolicy struct {
	Enabled     bool
	Strategy    string // disabled | random-range | bucketed
	MinBytes    int
	MaxBytes    int
	Probability float64 // 0.0-1.0 chance to pad (ignored when Min==Max>0 for exact length)
	Buckets     []int   // target sizes for bucketed strategy
}

// DefaultPaddingPolicy returns production defaults: enabled random-range padding.
func DefaultPaddingPolicy() PaddingPolicy {
	return PaddingPolicy{
		Enabled:     true,
		Strategy:    PaddingRandomRange,
		MinBytes:    0,
		MaxBytes:    64,
		Probability: 1.0,
	}
}

// Validate checks padding policy fields.
func (p PaddingPolicy) Validate() error {
	if p.MinBytes < 0 || p.MaxBytes < 0 {
		return fmt.Errorf("padding min/max must be >= 0")
	}
	if p.MaxBytes < p.MinBytes {
		return fmt.Errorf("padding max (%d) < min (%d)", p.MaxBytes, p.MinBytes)
	}
	if p.Probability < 0 || p.Probability > 1 {
		return fmt.Errorf("padding probability must be in [0,1]")
	}
	switch p.effectiveStrategy() {
	case PaddingDisabled, PaddingRandomRange:
		// ok
	case PaddingBucketed:
		if len(p.Buckets) == 0 {
			return fmt.Errorf("bucketed padding requires non-empty Buckets")
		}
		for _, b := range p.Buckets {
			if b < 0 {
				return fmt.Errorf("padding bucket size must be >= 0")
			}
		}
	default:
		return fmt.Errorf("unknown padding strategy %q", p.Strategy)
	}
	return nil
}

func (p PaddingPolicy) effectiveStrategy() string {
	if !p.Enabled {
		return PaddingDisabled
	}
	s := p.Strategy
	if s == "" {
		if p.MaxBytes > 0 || p.MinBytes > 0 {
			return PaddingRandomRange
		}
		return PaddingDisabled
	}
	return s
}

// wireOverheadEstimate is length(4)+epoch(4)+seq(8)+AEAD tag for size capping.
const wireOverheadEstimate = 4 + 4 + 8 + chacha20poly1305.Overhead

func (s *Session) maxPaddingBytes(payloadLen int) int {
	maxInner := packet.MaxInnerPayload - 4 - payloadLen
	if maxInner < 0 {
		return 0
	}
	limit := protocol.MaxFrameSize
	if s.cfg.MTU > 0 && s.cfg.MTU < limit {
		limit = s.cfg.MTU
	}
	// total wire ≈ overhead + inner; inner = 4 + payload + padding
	maxFromMTU := limit - wireOverheadEstimate - 4 - payloadLen
	if maxFromMTU < maxInner {
		maxInner = maxFromMTU
	}
	if maxInner < 0 {
		return 0
	}
	return maxInner
}

func (s *Session) buildPadding(payloadLen int) ([]byte, error) {
	p := s.cfg.PaddingPolicy
	strat := p.effectiveStrategy()
	if strat == PaddingDisabled {
		return nil, nil
	}

	capN := s.maxPaddingBytes(payloadLen)
	if capN <= 0 {
		return nil, nil
	}

	minB := p.MinBytes
	maxB := p.MaxBytes
	if maxB > capN {
		maxB = capN
	}
	if minB > maxB {
		minB = maxB
	}

	switch strat {
	case PaddingBucketed:
		return s.padBucketed(p.Buckets, minB, maxB, p.Probability, capN)
	default: // random-range
		return s.padRandomRange(minB, maxB, p.Probability)
	}
}

// buildHandshakePadding applies the session PaddingPolicy to pre-AEAD handshake messages.
func (s *Session) buildHandshakePadding() ([]byte, error) {
	p := s.cfg.PaddingPolicy
	strat := p.effectiveStrategy()
	if strat == PaddingDisabled {
		return nil, nil
	}
	// Cap against MaxHandshakeSize leaving room for fixed fields + pad_len(2).
	const worstFixed = 2 + 32 + 4 + 2 // resp fixed + pad header
	capN := protocol.MaxHandshakeSize - worstFixed
	if capN <= 0 {
		return nil, nil
	}
	minB := p.MinBytes
	maxB := p.MaxBytes
	if maxB > capN {
		maxB = capN
	}
	if minB > maxB {
		minB = maxB
	}
	switch strat {
	case PaddingBucketed:
		return s.padBucketed(p.Buckets, minB, maxB, p.Probability, capN)
	default:
		return s.padRandomRange(minB, maxB, p.Probability)
	}
}

func (s *Session) padRandomRange(minB, maxB int, prob float64) ([]byte, error) {
	if maxB < 0 || minB < 0 || minB > maxB {
		return nil, nil
	}
	// Exact length: when Min==Max>0 always emit that many bytes.
	if minB == maxB && minB > 0 {
		padding := make([]byte, minB)
		if _, err := rand.Read(padding); err != nil {
			return nil, err
		}
		return padding, nil
	}
	if maxB == 0 && minB == 0 {
		return nil, nil
	}
	if prob <= 0 {
		prob = 0.25
	}
	var coin [1]byte
	if _, err := rand.Read(coin[:]); err != nil {
		return nil, err
	}
	if float64(coin[0])/255.0 >= prob {
		return nil, nil
	}
	span := maxB - minB + 1
	off, err := cryptoRandN(span)
	if err != nil {
		return nil, err
	}
	n := minB + off
	if n <= 0 {
		return nil, nil
	}
	padding := make([]byte, n)
	if _, err := rand.Read(padding); err != nil {
		return nil, err
	}
	return padding, nil
}

func (s *Session) padBucketed(buckets []int, minB, maxB int, prob float64, capN int) ([]byte, error) {
	if prob <= 0 {
		prob = 0.25
	}
	var coin [1]byte
	if _, err := rand.Read(coin[:]); err != nil {
		return nil, err
	}
	if float64(coin[0])/255.0 >= prob {
		return nil, nil
	}
	var candidates []int
	for _, b := range buckets {
		if b < 0 || b > capN {
			continue
		}
		if b < minB || (maxB > 0 && b > maxB) {
			continue
		}
		candidates = append(candidates, b)
	}
	if len(candidates) == 0 {
		return s.padRandomRange(minB, maxB, 1.0)
	}
	idx, err := cryptoRandN(len(candidates))
	if err != nil {
		return nil, err
	}
	n := candidates[idx]
	if n <= 0 {
		return nil, nil
	}
	padding := make([]byte, n)
	if _, err := rand.Read(padding); err != nil {
		return nil, err
	}
	return padding, nil
}
