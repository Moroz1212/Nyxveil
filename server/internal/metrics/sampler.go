package metrics

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sampler collects lightweight host metrics without shelling out.
type Sampler struct {
	mu       sync.Mutex
	lastCPU  cpuSample
	lastNet  netSample
	lastTime time.Time
	RxRate   float64
	TxRate   float64
	CPU      float64
}

type cpuSample struct {
	idle, total uint64
}

type netSample struct {
	rx, tx uint64
}

func NewSampler() *Sampler {
	s := &Sampler{lastTime: time.Now()}
	s.lastCPU = readCPU()
	s.lastNet = readNet()
	return s
}

func (s *Sampler) Sample() (cpuPct, memPct float64, memBytes int64, rxRate, txRate float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	dt := now.Sub(s.lastTime).Seconds()
	if dt <= 0 {
		dt = 1
	}
	cpu := readCPU()
	idleDelta := float64(cpu.idle - s.lastCPU.idle)
	totalDelta := float64(cpu.total - s.lastCPU.total)
	if totalDelta > 0 {
		s.CPU = (1 - idleDelta/totalDelta) * 100
	}
	s.lastCPU = cpu

	net := readNet()
	s.RxRate = float64(net.rx-s.lastNet.rx) / dt
	s.TxRate = float64(net.tx-s.lastNet.tx) / dt
	s.lastNet = net
	s.lastTime = now

	memBytes, memPct = readMem()
	return s.CPU, memPct, memBytes, s.RxRate, s.TxRate
}

func readCPU() cpuSample {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cpuSample{}
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}
	}
	var vals []uint64
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		vals = append(vals, v)
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	return cpuSample{idle: idle, total: total}
}

func readMem() (bytes int64, pct float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return int64(ms.Sys), 0
	}
	defer f.Close()
	var total, avail uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			total = parseMemKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			avail = parseMemKB(line)
		}
	}
	if total == 0 {
		return 0, 0
	}
	used := (total - avail) * 1024
	pct = float64(total-avail) / float64(total) * 100
	return int64(used), pct
}

func parseMemKB(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

func readNet() netSample {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netSample{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var rx, tx uint64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.Contains(line, ":") || strings.HasPrefix(line, "Inter") || strings.HasPrefix(line, "face") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		if name == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return netSample{rx: rx, tx: tx}
}
