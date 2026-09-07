package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileRotation(t *testing.T) {
	// Avoid t.TempDir(): on Windows, delayed handle release after Close can make
	// testing's RemoveAll fail even when the test assertions passed.
	dir, err := os.MkdirTemp("", "nyxveil-diag-rot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer removeDirRetry(dir)

	fs, err := NewFileSink(dir, "t.log", 200, 3)
	if err != nil {
		t.Fatal(err)
	}

	line := strings.Repeat("x", 80)
	for i := 0; i < 20; i++ {
		if err := fs.WriteLine(line); err != nil {
			_ = fs.Close()
			t.Fatal(err)
		}
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "t.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "t.log.1")); err != nil {
		t.Fatalf("expected rotation file: %v", err)
	}
}

func removeDirRetry(dir string) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.RemoveAll(dir)
		if err == nil || time.Now().After(deadline) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
