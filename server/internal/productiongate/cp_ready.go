package productiongate

import (
	"encoding/json"
	"fmt"
	"time"
)

// CPReadyConfig mirrors production-gate.sh wait_cp_authenticated defaults.
type CPReadyConfig struct {
	Wait       time.Duration
	Interval   time.Duration
	NeedStable int
	FailFast   bool
}

// DefaultCPReady returns the production gate defaults (45s, 1s, 3 samples).
func DefaultCPReady() CPReadyConfig {
	return CPReadyConfig{
		Wait:       45 * time.Second,
		Interval:   time.Second,
		NeedStable: 3,
		FailFast:   false,
	}
}

// StatusSample is the machine-readable subset used for CP readiness.
type StatusSample struct {
	CPConnected bool   `json:"cp_connected"`
	CPURL       string `json:"cp_url"`
	CPLastError string `json:"cp_last_error"`
	Healthy     bool   `json:"healthy"`
}

// ParseStatusJSON extracts CP readiness fields from nyxveilctl status JSON.
func ParseStatusJSON(raw []byte) (StatusSample, error) {
	var s StatusSample
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}

// PermanentCPError reports hard config/TLS failures that should stop waiting.
func PermanentCPError(errText string) bool {
	if errText == "" {
		return false
	}
	needles := []string{
		"unsupported scheme",
		"URL missing host",
		"no such host",
		"certificate is not valid",
	}
	for _, n := range needles {
		if containsFold(errText, n) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	// tiny ASCII fold search
	ls, lsub := len(s), len(sub)
	for i := 0; i+lsub <= ls; i++ {
		ok := true
		for j := 0; j < lsub; j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// WaitCPAuthenticated polls fetch until cp_connected is stable.
// fetch returns status JSON bytes (same source as updater post-check).
func WaitCPAuthenticated(cfg CPReadyConfig, fetch func() ([]byte, error)) error {
	if cfg.NeedStable <= 0 {
		cfg.NeedStable = 1
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Wait <= 0 {
		cfg.Wait = 45 * time.Second
	}
	deadline := time.Now().Add(cfg.Wait)
	stable := 0
	var last error
	for time.Now().Before(deadline) {
		raw, err := fetch()
		if err != nil {
			stable = 0
			last = fmt.Errorf("status unavailable: %w", err)
			time.Sleep(cfg.Interval)
			continue
		}
		st, err := ParseStatusJSON(raw)
		if err != nil {
			stable = 0
			last = err
			time.Sleep(cfg.Interval)
			continue
		}
		if st.CPConnected {
			stable++
			if stable >= cfg.NeedStable {
				return nil
			}
		} else {
			stable = 0
			last = fmt.Errorf("cp_connected=false err=%s", st.CPLastError)
			if cfg.FailFast && PermanentCPError(st.CPLastError) {
				return last
			}
		}
		time.Sleep(cfg.Interval)
	}
	if last == nil {
		last = fmt.Errorf("timeout waiting for authenticated Control Plane")
	}
	return fmt.Errorf("control_plane_reachable: %w", last)
}

// UpdaterManagementConnected mirrors health.EvaluatePostUpdate field.
// Authoritative gate must agree when this is true and status has not regressed.
func UpdaterAndGateAgree(updaterManagementConnected bool, statusJSON []byte) error {
	st, err := ParseStatusJSON(statusJSON)
	if err != nil {
		return err
	}
	if updaterManagementConnected && !st.CPConnected {
		return fmt.Errorf("invariant broken: updater management_plane_connected=true but status.cp_connected=false")
	}
	return nil
}
