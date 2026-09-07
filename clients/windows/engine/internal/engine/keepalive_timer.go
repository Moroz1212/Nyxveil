package engine

import "time"

func newTimer(d time.Duration) *time.Timer { return time.NewTimer(d) }

func stopTimer(t *time.Timer) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
