package replay

import (
	"fmt"
	"sync"
)

// Window implements sliding-window replay protection with epoch support.
// bitmap[i] is true if sequence (highest - i) has been seen.
type Window struct {
	mu         sync.Mutex
	epoch      uint32
	highest    uint64
	have       bool
	bitmap     []bool
	windowSize uint64
}

// NewWindow creates a replay window with the given size.
func NewWindow(windowSize uint64) *Window {
	if windowSize == 0 {
		windowSize = 1024
	}
	return &Window{
		windowSize: windowSize,
		bitmap:     make([]bool, windowSize),
	}
}

// Epoch returns the current replay window epoch.
func (w *Window) Epoch() uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.epoch
}

// Reset clears state for a new epoch.
func (w *Window) Reset(epoch uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epoch = epoch
	w.highest = 0
	w.have = false
	for i := range w.bitmap {
		w.bitmap[i] = false
	}
}

// CheckAndMark validates sequence for epoch and marks it as seen.
// Returns error for duplicate, too old, or invalid epoch.
func (w *Window) CheckAndMark(epoch uint32, sequence uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkAndMarkLocked(epoch, sequence)
}

func (w *Window) checkAndMarkLocked(epoch uint32, sequence uint64) error {
	if epoch < w.epoch {
		return fmt.Errorf("replay: epoch too old (%d < %d)", epoch, w.epoch)
	}
	if epoch > w.epoch {
		w.epoch = epoch
		w.highest = 0
		w.have = false
		for i := range w.bitmap {
			w.bitmap[i] = false
		}
	}

	if !w.have {
		w.highest = sequence
		w.have = true
		for i := range w.bitmap {
			w.bitmap[i] = false
		}
		w.bitmap[0] = true
		return nil
	}

	if sequence > w.highest {
		advance := sequence - w.highest
		if advance >= w.windowSize {
			for i := range w.bitmap {
				w.bitmap[i] = false
			}
		} else {
			for i := w.windowSize - 1; i >= advance; i-- {
				w.bitmap[i] = w.bitmap[i-advance]
			}
			for i := uint64(0); i < advance; i++ {
				w.bitmap[i] = false
			}
		}
		w.highest = sequence
		w.bitmap[0] = true
		return nil
	}

	delta := w.highest - sequence
	if delta >= w.windowSize {
		return fmt.Errorf("replay: sequence too old (%d)", sequence)
	}
	if w.bitmap[delta] {
		return fmt.Errorf("replay: duplicate sequence %d", sequence)
	}
	w.bitmap[delta] = true
	return nil
}

// Clone copies epoch, highest, and bitmap into a new window (for rekey overlap).
func (w *Window) Clone() *Window {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := NewWindow(w.windowSize)
	out.epoch = w.epoch
	out.highest = w.highest
	out.have = w.have
	copy(out.bitmap, w.bitmap)
	return out
}
