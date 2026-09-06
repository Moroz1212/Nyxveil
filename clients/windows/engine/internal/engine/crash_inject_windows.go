//go:build windows

package engine

import (
	"fmt"
	"os"
	"strings"
)

// CrashAfterEnv enables test-harness-only fault injection.
// Values: bypass | tun_addr | tun_dns | ipv6 | default_vpn
// After journal Pending + successful OS mutation for that step, process exits 99.
const CrashAfterEnv = "NYXVEIL_CRASH_AFTER"

func crashAfter(step string) {
	want := strings.TrimSpace(os.Getenv(CrashAfterEnv))
	if want == "" || !strings.EqualFold(want, step) {
		return
	}
	// Distinctive markers for elevated gate (must appear before intentional exit 99).
	fmt.Fprintf(os.Stderr, "NYXVEIL_CRASH_CHECKPOINT=%s\n", step)
	fmt.Fprintf(os.Stdout, "NYXVEIL_CRASH_CHECKPOINT=%s\n", step)
	fmt.Fprintf(os.Stderr, "nyxveil: fault-inject crash after %s\n", step)
	os.Exit(99)
}
