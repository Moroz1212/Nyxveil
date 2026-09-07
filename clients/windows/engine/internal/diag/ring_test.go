package diag

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRingMaxSizeAndOrder(t *testing.T) {
	r := NewRing(8)
	for i := 0; i < 20; i++ {
		r.Append(Event{Time: time.Unix(int64(i), 0), Level: LevelInfo, Component: "T", Event: "e", Message: string(rune('a' + i%26))})
	}
	if r.Len() != 8 {
		t.Fatalf("len=%d want 8", r.Len())
	}
	snap := r.Snapshot()
	if snap[0].Message != string(rune('a'+12)) { // 20 events 0..19, keep 12..19
		// i=12 → 'a'+12 = 'm'
		if snap[0].Time.Unix() != 12 {
			t.Fatalf("oldest time=%d want 12", snap[0].Time.Unix())
		}
	}
	if snap[len(snap)-1].Time.Unix() != 19 {
		t.Fatalf("newest=%d", snap[len(snap)-1].Time.Unix())
	}
}

func TestRingThreadSafety(t *testing.T) {
	r := NewRing(256)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Append(Event{Level: LevelInfo, Component: "T", Event: "x", Message: "ok"})
			}
		}(g)
	}
	wg.Wait()
	if r.Len() == 0 || r.Len() > r.Cap() {
		t.Fatalf("len=%d cap=%d", r.Len(), r.Cap())
	}
}

func TestRingSubscribe(t *testing.T) {
	r := NewRing(64)
	id, ch := r.Subscribe()
	defer r.Unsubscribe(id)
	r.Append(Event{Level: LevelWarn, Component: "S", Event: "ping", Message: "hi"})
	select {
	case e := <-ch:
		if e.Event != "ping" {
			t.Fatalf("%+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
}

func TestSanitizeSecrets(t *testing.T) {
	cases := []struct {
		in   string
		deny string
	}{
		{"Authorization: Bearer abc.def.ghi", "abc.def"},
		{"X-License-Token: supersecretvalue", "supersecretvalue"},
		{"nyx_lic_abc123:the_secret_part", "the_secret_part"},
		{"rvpn_access_ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ABCDEFGHIJKLMNOP"},
		{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signaturehere", "eyJhbGciOiJIUzI1NiJ9"},
		{"device_private_key=0123456789abcdef0123456789abcdef", "0123456789abcdef"},
	}
	for _, tc := range cases {
		out := Sanitize(tc.in)
		if strings.Contains(out, tc.deny) {
			t.Fatalf("sanitize failed for %q → %q still has %q", tc.in, out, tc.deny)
		}
		if !strings.Contains(out, "REDACTED") && !strings.Contains(out, "[REDACTED") {
			t.Fatalf("expected REDACTED in %q", out)
		}
	}
}

func TestEventFormat(t *testing.T) {
	e := Event{
		Time:      time.Date(2026, 9, 7, 20, 10, 31, 441000000, time.Local),
		Level:     LevelInfo,
		Component: "SESSION",
		Event:     "Connect",
		Message:   "loc=fi-helsinki",
	}
	s := e.Format()
	if !strings.Contains(s, "INFO") || !strings.Contains(s, "SESSION") || !strings.Contains(s, "Connect") {
		t.Fatalf("%q", s)
	}
}
