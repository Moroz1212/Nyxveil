package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSink writes rotated plain-text logs under ProgramData.
type FileSink struct {
	mu       sync.Mutex
	dir      string
	baseName string
	maxBytes int64
	maxFiles int
	cur      *os.File
	size     int64
}

// DefaultLogDir is the production service log directory.
func DefaultLogDir() string {
	return filepath.Join(os.Getenv("ProgramData"), "Nyxveil", "Client", "logs")
}

// NewFileSink opens (or creates) a rotating log writer.
func NewFileSink(dir, baseName string, maxBytes int64, maxFiles int) (*FileSink, error) {
	if dir == "" {
		dir = DefaultLogDir()
	}
	if baseName == "" {
		baseName = "nyxveil-service.log"
	}
	if maxBytes <= 0 {
		maxBytes = 3 << 20 // 3 MiB
	}
	if maxFiles < 2 {
		maxFiles = 5
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	fs := &FileSink{dir: dir, baseName: baseName, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := fs.openCurrent(); err != nil {
		return nil, err
	}
	return fs, nil
}

func (f *FileSink) path() string { return filepath.Join(f.dir, f.baseName) }

func (f *FileSink) openCurrent() error {
	p := f.path()
	fi, err := os.Stat(p)
	size := int64(0)
	if err == nil {
		size = fi.Size()
	}
	file, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if f.cur != nil {
		_ = f.cur.Close()
	}
	f.cur = file
	f.size = size
	return nil
}

// WriteLine appends one line and rotates when needed.
func (f *FileSink) WriteLine(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur == nil {
		if err := f.openCurrent(); err != nil {
			return err
		}
	}
	b := []byte(line + "\n")
	if f.size+int64(len(b)) > f.maxBytes {
		if err := f.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := f.cur.Write(b)
	f.size += int64(n)
	return err
}

func (f *FileSink) rotateLocked() error {
	if f.cur != nil {
		_ = f.cur.Close()
		f.cur = nil
	}
	// Shift: .4 → delete, .3 → .4, …, current → .1
	for i := f.maxFiles - 1; i >= 1; i-- {
		src := f.path()
		if i > 1 {
			src = fmt.Sprintf("%s.%d", f.path(), i-1)
		}
		dst := fmt.Sprintf("%s.%d", f.path(), i)
		_ = os.Remove(dst)
		_ = os.Rename(src, dst)
	}
	return f.openCurrent()
}

// Close closes the current file handle.
func (f *FileSink) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cur == nil {
		return nil
	}
	_ = f.cur.Sync()
	err := f.cur.Close()
	f.cur = nil
	f.size = 0
	return err
}

// EnsureDirReady creates the log directory (for service startup).
func EnsureDirReady() error {
	return os.MkdirAll(DefaultLogDir(), 0o755)
}

// Stamp is a helper for tests.
func Stamp() time.Time { return time.Now() }
