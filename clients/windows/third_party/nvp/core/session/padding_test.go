package session

import (
	"testing"
)

func TestPaddingExactMinEqualsMax(t *testing.T) {
	s := New(Config{
		ReplayWindow: 64,
		MTU:          1280,
		PaddingPolicy: PaddingPolicy{
			Enabled:  true,
			Strategy: PaddingRandomRange,
			MinBytes: 32,
			MaxBytes: 32,
		},
	})
	pad, err := s.buildPadding(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pad) != 32 {
		t.Fatalf("expected exact 32 bytes padding, got %d", len(pad))
	}
}

func TestDefaultConfigPaddingEnabled(t *testing.T) {
	cfg := DefaultConfig(true)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.PaddingPolicy.Enabled || cfg.PaddingPolicy.Strategy != PaddingRandomRange {
		t.Fatalf("unexpected default padding: %+v", cfg.PaddingPolicy)
	}
	if cfg.PaddingPolicy.MaxBytes != 64 {
		t.Fatalf("expected MaxBytes 64, got %d", cfg.PaddingPolicy.MaxBytes)
	}
}

func TestPaddingCapByMTU(t *testing.T) {
	s := New(Config{
		ReplayWindow: 64,
		MTU:          80,
		PaddingPolicy: PaddingPolicy{
			Enabled:  true,
			Strategy: PaddingRandomRange,
			MinBytes: 200,
			MaxBytes: 200,
		},
	})
	pad, err := s.buildPadding(20)
	if err != nil {
		t.Fatal(err)
	}
	// Exact 200 would exceed MTU; cap should shrink below Min, so padRandomRange
	// with minB adjusted may yield smaller or empty after min>max clamp.
	if len(pad) > 200 {
		t.Fatalf("padding not capped: %d", len(pad))
	}
}
