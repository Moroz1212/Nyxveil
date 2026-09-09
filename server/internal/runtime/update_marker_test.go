package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteUpdateMarkerAtomicAndReadable(t *testing.T) {
	dir := t.TempDir()
	n := &Node{}
	n.opts.KeyPath = filepath.Join(dir, "node.key")
	m := updateMarker{
		CommandID:       "cmd-1",
		PreviousVersion: "1.1.10",
		TargetVersion:   "1.1.11",
		Phase:           "Downloading",
		StartedAt:       "2026-01-01T00:00:00Z",
	}
	if err := n.writeUpdateMarker(m); err != nil {
		t.Fatal(err)
	}
	got, ok := n.readUpdateMarker()
	if !ok {
		t.Fatal("marker missing")
	}
	if got.TargetVersion != "1.1.11" || got.CommandID != "cmd-1" {
		t.Fatalf("unexpected marker %+v", got)
	}
	st, err := os.Stat(n.updateMarkerPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("marker mode too open: %v", st.Mode())
	}
	pinned, ok := ReadPinnedUpdateTarget(dir)
	if !ok || pinned != "1.1.11" {
		t.Fatalf("pinned=%q ok=%v", pinned, ok)
	}
}
