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
	fmt.Fprintf(os.Stderr, "nyxveil: fault-inject crash after %s\n", step)
	os.Exit(99)
}
