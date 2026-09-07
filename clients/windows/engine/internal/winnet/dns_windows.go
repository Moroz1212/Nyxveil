//go:build windows

package winnet

import (
	"fmt"
	"os/exec"
	"strings"
)
// AdapterExistsByAlias reports whether a network adapter with the given Name exists.
func AdapterExistsByAlias(alias string) (bool, error) {
	if strings.TrimSpace(alias) == "" {
		return false, fmt.Errorf("winnet: adapter alias required")
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`$a = Get-NetAdapter -Name '%s' -ErrorAction SilentlyContinue; `+
			`if ($null -eq $a) { '0' } else { '1' }`,
		escapePSSingleQuoted(alias),
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("winnet: Get-NetAdapter Alias=%q: %w (%s)", alias, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)) == "1", nil
}

// ClearDNSServersOnAlias clears IPv4 DNS on an adapter via PowerShell
// (locale-independent; replaces brittle netsh "delete dns ... all").
// Missing adapter is treated as already-clean (idempotent recovery).
func ClearDNSServersOnAlias(alias string) error {
	if strings.TrimSpace(alias) == "" {
		return fmt.Errorf("winnet: adapter alias required")
	}
	exists, err := AdapterExistsByAlias(alias)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`Set-DnsClientServerAddress -InterfaceAlias '%s' -ResetServerAddresses -ErrorAction Stop`,
		escapePSSingleQuoted(alias),
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		raw := strings.TrimSpace(string(out))
		// Race: adapter disappeared between existence check and clear.
		if strings.Contains(raw, "ObjectNotFound") || strings.Contains(raw, "CmdletizationQuery_NotFound") {
			return nil
		}
		return fmt.Errorf("winnet: ResetServerAddresses Alias=%q: %w (%s)", alias, err, raw)
	}
	return nil
}

// SetDNSServersOnAlias sets static IPv4 DNS servers on an adapter (locale-independent).
func SetDNSServersOnAlias(alias string, servers []string) error {
	if strings.TrimSpace(alias) == "" {
		return fmt.Errorf("winnet: adapter alias required")
	}
	if len(servers) == 0 {
		return ClearDNSServersOnAlias(alias)
	}
	quoted := make([]string, len(servers))
	for i, s := range servers {
		quoted[i] = "'" + escapePSSingleQuoted(s) + "'"
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`Set-DnsClientServerAddress -InterfaceAlias '%s' -ServerAddresses @(%s) -ErrorAction Stop`,
		escapePSSingleQuoted(alias), strings.Join(quoted, ","),
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("winnet: Set-DnsClientServerAddress Alias=%q: %w (%s)", alias, err, strings.TrimSpace(string(out)))
	}
	return nil
}
