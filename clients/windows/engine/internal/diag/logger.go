package diag

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// Logger is the process-wide diagnostic sink (ring + optional file + live hook).
type Logger struct {
	ring *Ring
	file *FileSink

	hookMu sync.RWMutex
	hook   func(Event) // optional IPC broadcast
}

var (
	defaultMu sync.RWMutex
	defaultL  *Logger
)

// Default returns the process logger, creating an in-memory-only instance if needed.
func Default() *Logger {
	defaultMu.RLock()
	l := defaultL
	defaultMu.RUnlock()
	if l != nil {
		return l
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultL == nil {
		defaultL = &Logger{ring: NewRing(DefaultCap)}
	}
	return defaultL
}

// InitDefault installs ring + optional file sink as the process default.
func InitDefault(file *FileSink) *Logger {
	l := &Logger{ring: NewRing(DefaultCap), file: file}
	defaultMu.Lock()
	defaultL = l
	defaultMu.Unlock()
	return l
}

// SetHook registers a non-blocking live fan-out (e.g. IPC broadcast).
func (l *Logger) SetHook(fn func(Event)) {
	l.hookMu.Lock()
	l.hook = fn
	l.hookMu.Unlock()
}

// Ring exposes the buffer for IPC snapshot/subscribe.
func (l *Logger) Ring() *Ring { return l.ring }

// Log records a diagnostic event.
func (l *Logger) Log(level Level, component, event, message string) {
	if l == nil {
		return
	}
	e := Event{
		Time:      time.Now(),
		Level:     level,
		Component: Sanitize(component),
		Event:     Sanitize(event),
		Message:   Sanitize(message),
	}
	l.ring.Append(e)
	line := e.Format()
	if l.file != nil {
		_ = l.file.WriteLine(line)
	}
	l.hookMu.RLock()
	h := l.hook
	l.hookMu.RUnlock()
	if h != nil {
		h(e)
	}
}

func (l *Logger) Info(component, event, message string) {
	l.Log(LevelInfo, component, event, message)
}
func (l *Logger) Warn(component, event, message string) {
	l.Log(LevelWarn, component, event, message)
}
func (l *Logger) Error(component, event, message string) {
	l.Log(LevelError, component, event, message)
}

func (l *Logger) InfoFields(component, event string, kv map[string]string) {
	l.Info(component, event, Fields(kv))
}

func Info(component, event, message string)  { Default().Info(component, event, message) }
func Warn(component, event, message string)  { Default().Warn(component, event, message) }
func Error(component, event, message string) { Default().Error(component, event, message) }
func InfoFields(component, event string, kv map[string]string) {
	Default().InfoFields(component, event, kv)
}

func (l *Logger) CloseFile() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// MustInitFile best-effort opens ProgramData logs; falls back to ring-only.
func MustInitFile() *Logger {
	_ = EnsureDirReady()
	fs, err := NewFileSink(DefaultLogDir(), "nyxveil-service.log", 3<<20, 5)
	if err != nil {
		l := InitDefault(nil)
		l.Warn("DIAG", "file_open_failed", err.Error())
		return l
	}
	l := InitDefault(fs)
	l.Info("DIAG", "file_ready", fmt.Sprintf("path=%s", filepath.Join(DefaultLogDir(), "nyxveil-service.log")))
	return l
}
