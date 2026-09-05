package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// APITime is a Control Plane JSON timestamp.
// Accepts RFC3339 / RFC3339Nano with Z or numeric offset, and legacy .NET
// DateTime strings without a timezone (interpreted strictly as UTC).
type APITime time.Time

func (t APITime) Time() time.Time { return time.Time(t) }

func (t APITime) IsZero() bool { return time.Time(t).IsZero() }

func (t APITime) MarshalJSON() ([]byte, error) {
	tt := time.Time(t).UTC()
	if tt.IsZero() {
		return []byte(`null`), nil
	}
	return json.Marshal(tt.Format(time.RFC3339Nano))
}

func (t *APITime) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*t = APITime{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("controlplane: APITime: %w", err)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*t = APITime{}
		return nil
	}
	parsed, err := ParseAPITime(s)
	if err != nil {
		return err
	}
	*t = APITime(parsed)
	return nil
}

// ParseAPITime parses a Control Plane timestamp string as UTC.
func ParseAPITime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("controlplane: empty APITime")
	}

	// Reject obviously non-ISO forms.
	if strings.ContainsAny(s, "/ ") || strings.Contains(strings.ToLower(s), "am") || strings.Contains(strings.ToLower(s), "pm") {
		return time.Time{}, fmt.Errorf("controlplane: rejected APITime format %q", s)
	}

	layoutsZoned := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05.9999999Z07:00", // .NET 7-digit fraction + offset/Z
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02T15:04:05.999Z07:00",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layoutsZoned {
		if tt, err := time.Parse(layout, s); err == nil {
			return tt.UTC(), nil
		}
	}

	// Legacy .NET System.Text.Json DateTime (Unspecified) — no zone suffix.
	// Interpret STRICTLY as UTC (never local).
	if hasZoneSuffix(s) {
		return time.Time{}, fmt.Errorf("controlplane: cannot parse APITime %q", s)
	}
	layoutsUTC := []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05.9999999", // .NET 7 fractional digits
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05.999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layoutsUTC {
		if tt, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return tt.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("controlplane: cannot parse APITime %q", s)
}

func hasZoneSuffix(s string) bool {
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		return true
	}
	// ±HH:MM or ±HHMM after the time portion
	if i := strings.LastIndexAny(s, "+-"); i > 10 {
		return true
	}
	return false
}
