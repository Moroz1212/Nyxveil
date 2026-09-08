package runtime

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestCommandDedupeNeverDoubleExecutes(t *testing.T) {
	dir := t.TempDir()
	store := newCommandDedupeStore(filepath.Join(dir, "commands-state.json"))
	id := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	if !store.TryMarkExecuted(id) {
		t.Fatal("first mark should succeed")
	}
	if store.TryMarkExecuted(id) {
		t.Fatal("second mark must be rejected")
	}

	// Concurrent attempts must still allow only one execution slot.
	var wg sync.WaitGroup
	wins := 0
	var mu sync.Mutex
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if store.TryMarkExecuted(id) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 0 {
		t.Fatalf("concurrent wins=%d want 0", wins)
	}

	other := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	if !store.TryMarkExecuted(other) {
		t.Fatal("different id should succeed")
	}
}

func TestRebootPendingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newCommandDedupeStore(filepath.Join(dir, "commands-state.json"))
	store.addRebootPending("cmd-1", "boot-before")
	pending := store.rebootPending()
	if len(pending) != 1 || pending[0].PreBootID != "boot-before" {
		t.Fatalf("%+v", pending)
	}
	store.removeRebootPending("cmd-1")
	if len(store.rebootPending()) != 0 {
		t.Fatal("expected cleared reboot pending")
	}
}

func TestRebootPendingSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands-state.json")
	store := newCommandDedupeStore(path)
	store.addRebootPending("cmd-2", "boot-x")
	reloaded := newCommandDedupeStore(path)
	pending := reloaded.rebootPending()
	if len(pending) != 1 || pending[0].CommandID != "cmd-2" {
		t.Fatalf("%+v", pending)
	}
}
