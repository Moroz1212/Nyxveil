package replay_test

import (
	"testing"

	"github.com/nyxveil/nvp/core/replay"
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

func TestReplayWindowRejectsReplayAfterAdvance(t *testing.T) {
	w := replay.NewWindow(64)
	w.Reset(1)
	if err := w.CheckAndMark(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAndMark(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAndMark(1, 0); err == nil {
		t.Fatal("seq 0 must stay marked after window advance")
	}
	for i := uint64(2); i < 20; i++ {
		if err := w.CheckAndMark(1, i); err != nil {
			t.Fatalf("seq %d: %v", i, err)
		}
	}
	if err := w.CheckAndMark(1, 5); err == nil {
		t.Fatal("seq 5 must remain rejected after further advance")
	}
}

func TestReplayWindowGapFillAndTooOld(t *testing.T) {
	w := replay.NewWindow(8)
	w.Reset(1)
	if err := w.CheckAndMark(1, 10); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAndMark(1, 7); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckAndMark(1, 7); err == nil {
		t.Fatal("duplicate gap fill")
	}
	if err := w.CheckAndMark(1, 1); err == nil {
		t.Fatal("sequence outside window must be too old")
	}
}

func TestReplayWindowClonePreservesSeen(t *testing.T) {
	w := replay.NewWindow(16)
	w.Reset(2)
	_ = w.CheckAndMark(2, 3)
	_ = w.CheckAndMark(2, 4)
	c := w.Clone()
	if err := c.CheckAndMark(2, 3); err == nil {
		t.Fatal("clone must retain seen sequences")
	}
	if err := c.CheckAndMark(2, 2); err != nil {
		t.Fatal(err)
	}
}
