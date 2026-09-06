package engine_test

import (
	"errors"
	"testing"

	"github.com/nyxveil/client-windows/internal/engine"
)

// RecordingApplier simulates transactional OS mutations for unit tests.
type RecordingApplier struct {
	FailAt string
	Steps  []string
	Stack  []string
}

func (r *RecordingApplier) Capture(p *engine.Plan) error {
	r.Steps = append(r.Steps, "capture")
	r.Stack = append(r.Stack, "capture")
	return nil
}
func (r *RecordingApplier) ApplyBypass(p *engine.Plan) error {
	if r.FailAt == "bypass" {
		_ = r.Restore(p)
		return errors.New("bypass fail")
	}
	r.Steps = append(r.Steps, "bypass")
	r.Stack = append(r.Stack, "bypass")
	return nil
}
func (r *RecordingApplier) ApplyTunnel(p *engine.Plan) error {
	if r.FailAt == "tunnel" {
		_ = r.Restore(p)
		return errors.New("tunnel fail")
	}
	r.Steps = append(r.Steps, "tunnel")
	r.Stack = append(r.Stack, "tunnel")
	p.DefaultViaTUN = true
	return nil
}
func (r *RecordingApplier) Restore(p *engine.Plan) error {
	for i := len(r.Stack) - 1; i >= 0; i-- {
		r.Steps = append(r.Steps, "undo:"+r.Stack[i])
	}
	r.Stack = nil
	p.DefaultViaTUN = false
	return nil
}

func TestRouteTransactionRollbackOnTunnelFailure(t *testing.T) {
	a := &RecordingApplier{FailAt: "tunnel"}
	p := engine.NewPlan()
	_ = a.Capture(p)
	_ = a.ApplyBypass(p)
	err := a.ApplyTunnel(p)
	if err == nil {
		t.Fatal("expected tunnel failure")
	}
	// Expect undo of bypass+capture after tunnel fail.
	joined := ""
	for _, s := range a.Steps {
		joined += s + ","
	}
	if !containsAll(a.Steps, "capture", "bypass", "undo:bypass", "undo:capture") {
		t.Fatalf("steps=%v", a.Steps)
	}
}

func containsAll(steps []string, want ...string) bool {
	have := map[string]bool{}
	for _, s := range steps {
		have[s] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}
