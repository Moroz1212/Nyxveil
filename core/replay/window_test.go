package replay_test

import (
	"testing"

	"github.com/nyxveil/nvp/replay"
)

func TestReplayWindowAcceptsInOrder(t *testing.T) {
	w := replay.NewWindow(64)
	w.Reset(1)
	for i := uint64(0); i < 10; i++ {
		if err := w.CheckAndMark(1, i); err != nil {
			t.Fatalf("seq %d: %v", i, err)
		}
	}
}

func TestReplayWindowRejectsDuplicate(t *testing.T) {
	w := replay.NewWindow(64)
	w.Reset(1)
	_ = w.CheckAndMark(1, 5)
	if err := w.CheckAndMark(1, 5); err == nil {
		t.Fatal("duplicate should be rejected")
	}
}

func TestReplayWindowRejectsOldEpoch(t *testing.T) {
	w := replay.NewWindow(64)
	w.Reset(5)
	if err := w.CheckAndMark(3, 0); err == nil {
		t.Fatal("old epoch should be rejected")
	}
}

func TestReplayWindowAcceptsReorder(t *testing.T) {
	w := replay.NewWindow(64)
	w.Reset(1)
	_ = w.CheckAndMark(1, 10)
	_ = w.CheckAndMark(1, 8)
	if err := w.CheckAndMark(1, 8); err == nil {
		t.Fatal("duplicate after reorder should fail")
	}
}
