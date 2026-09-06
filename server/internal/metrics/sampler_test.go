package metrics

import (
	"testing"
	"time"
)

func TestSamplerCPUWarmupAndMinimumInterval(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cpu := cpuSample{idle: 100, total: 200}
	net := netSample{rx: 1_000, tx: 2_000}
	s := &Sampler{
		lastCPU:  cpu,
		lastNet:  net,
		lastTime: now,
		now:      func() time.Time { return now },
		readCPU:  func() cpuSample { return cpu },
		readNet:  func() netSample { return net },
	}

	got, _, _, _, _ := s.Sample()
	if got != -1 || s.Ready() {
		t.Fatalf("first sample cpu=%v ready=%v; want unknown", got, s.Ready())
	}

	now = now.Add(250 * time.Millisecond)
	cpu = cpuSample{idle: 110, total: 225}
	got, _, _, _, _ = s.Sample()
	if got != -1 || s.Ready() {
		t.Fatalf("short interval cpu=%v ready=%v; want unknown", got, s.Ready())
	}

	now = now.Add(500 * time.Millisecond)
	cpu = cpuSample{idle: 130, total: 275}
	net = netSample{rx: 1_750, tx: 2_750}
	got, _, _, rx, tx := s.Sample()
	if !s.Ready() {
		t.Fatal("sampler should be ready after a sufficient interval")
	}
	if got != 60 {
		t.Fatalf("cpu=%v want 60", got)
	}
	if rx != 1_000 || tx != 1_000 {
		t.Fatalf("rates rx=%v tx=%v want 1000", rx, tx)
	}
}
