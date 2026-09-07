package diag

import (
	"fmt"
	"strings"
	"time"
)

// Level is a human-readable severity.
type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Event is one diagnostic line (never contains raw secrets after Sanitize).
type Event struct {
	Time      time.Time `json:"time"`
	Level     Level     `json:"level"`
	Component string    `json:"component"`
	Event     string    `json:"event"`
	Message   string    `json:"message"`
}

// Format returns a single human-readable line for GUI/file.
func (e Event) Format() string {
	ts := e.Time.Local().Format("2006-01-02 15:04:05.000")
	comp := e.Component
	if comp == "" {
		comp = "-"
	}
	ev := e.Event
	if ev == "" {
		ev = "-"
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		return fmt.Sprintf("%s %-5s %-12s %s", ts, e.Level, comp, ev)
	}
	return fmt.Sprintf("%s %-5s %-12s %s %s", ts, e.Level, comp, ev, msg)
}

// Fields builds a compact key=value message (values already expected sanitized).
func Fields(kv map[string]string) string {
	if len(kv) == 0 {
		return ""
	}
	parts := make([]string, 0, len(kv))
	// Stable-ish order: sort keys for tests.
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		parts = append(parts, k+"="+Sanitize(kv[k]))
	}
	return strings.Join(parts, " ")
}
