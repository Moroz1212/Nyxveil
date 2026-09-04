package replay

import (
	"fmt"
	"sync"
)

// Window implements sliding-window replay protection with epoch support.
type Window struct {
	mu         sync.Mutex
	epoch      uint32
	highest    uint64
	bitmap     []bool
	windowSize uint64
	transition *TransitionWindow
}

// TransitionWindow allows brief overlap during rekey between two epochs.
type TransitionWindow struct {
	prevEpoch  uint32
	prevWindow *Window
	deadline   int64 // unix nano, 0 = inactive
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
	for i := range w.bitmap {
		w.bitmap[i] = false
	}
}

// CheckAndMark validates sequence for epoch and marks it as seen.
// Returns error for duplicate, too old, or invalid epoch.
func (w *Window) CheckAndMark(epoch uint32, sequence uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.transition != nil && w.transition.prevWindow != nil {
		if epoch == w.transition.prevEpoch {
			return w.transition.prevWindow.checkAndMarkLocked(epoch, sequence)
		}
	}

	return w.checkAndMarkLocked(epoch, sequence)
}

func (w *Window) checkAndMarkLocked(epoch uint32, sequence uint64) error {
	if epoch < w.epoch {
		return fmt.Errorf("replay: epoch too old (%d < %d)", epoch, w.epoch)
	}
	if epoch > w.epoch {
		// New epoch - reset window
		w.epoch = epoch
		w.highest = 0
		for i := range w.bitmap {
			w.bitmap[i] = false
		}
	}

	if sequence+w.windowSize <= w.highest {
		return fmt.Errorf("replay: sequence too old (%d)", sequence)
	}

	if sequence <= w.highest {
		idx := int((w.highest - sequence) % w.windowSize)
		if w.bitmap[idx] {
			return fmt.Errorf("replay: duplicate sequence %d", sequence)
		}
		w.bitmap[idx] = true
		return nil
	}

	// Advance window
	advance := sequence - w.highest
	if advance >= w.windowSize {
		for i := range w.bitmap {
			w.bitmap[i] = false
		}
	} else {
		for i := uint64(0); i < advance; i++ {
			shiftIdx := int((w.windowSize - 1 - i) % w.windowSize)
			w.bitmap[shiftIdx] = false
		}
	}
	w.highest = sequence
	w.bitmap[0] = true
	return nil
}

// BeginTransition starts accepting packets from previous epoch during rekey overlap.
func (w *Window) BeginTransition(prevEpoch uint32, prev *Window) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.transition = &TransitionWindow{
		prevEpoch:  prevEpoch,
		prevWindow: prev,
	}
}

// EndTransition ends rekey transition window.
func (w *Window) EndTransition() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.transition = nil
}
