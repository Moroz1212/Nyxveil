//go:build windows

package winnet

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// IPv6State captures reversible per-interface IPv6 binding enablement (ms_tcpip6).
type IPv6State struct {
	InterfaceIndex uint32
	InterfaceName  string
	Enabled        bool
	Captured       bool
}

type netAdapterBindingJSON struct {
	Enabled       bool   `json:"Enabled"`
	InterfaceAlias string `json:"Name"`
	ComponentID   string `json:"ComponentID"`
}

// CaptureIPv6State uses structured PowerShell Get-NetAdapterBinding (locale-independent).
// Does not parse localized netsh text.
func CaptureIPv6State(ifIndex uint32) (IPv6State, error) {
	st := IPv6State{InterfaceIndex: ifIndex}
	if ifIndex == 0 {
		return st, nil
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`Get-NetAdapterBinding -InterfaceIndex %d -ComponentID ms_tcpip6 -ErrorAction Stop | `+
			`Select-Object Enabled,Name,ComponentID | ConvertTo-Json -Compress`,
		ifIndex,
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return st, fmt.Errorf("winnet: Get-NetAdapterBinding ms_tcpip6: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return st, fmt.Errorf("winnet: empty Get-NetAdapterBinding output for ifIndex %d", ifIndex)
	}
	var one netAdapterBindingJSON
	if err := json.Unmarshal([]byte(raw), &one); err != nil {
		// PowerShell may emit a single-object array
		var many []netAdapterBindingJSON
		if err2 := json.Unmarshal([]byte(raw), &many); err2 != nil || len(many) == 0 {
			return st, fmt.Errorf("winnet: parse binding JSON: %v / %v (%s)", err, err2, raw)
		}
		one = many[0]
	}
	st.Captured = true
	st.Enabled = one.Enabled
	st.InterfaceName = one.InterfaceAlias
	return st, nil
}

// ListActiveEgressIfIndexes returns ifIndex of Up adapters excluding the Nyxveil tunnel
// and loopback/virtual tunnel classes (locale-independent structured PowerShell).
func ListActiveEgressIfIndexes(excludeAlias string) ([]uint32, error) {
	excl := strings.ReplaceAll(excludeAlias, "'", "''")
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`$ex='%s'; `+
			`@(Get-NetAdapter -ErrorAction Stop | Where-Object { `+
			`  $_.Status -eq 'Up' -and $_.Name -ne $ex -and $_.InterfaceDescription -notmatch 'Wintun|Loopback|Nyxveil' `+
			`} | Select-Object -ExpandProperty ifIndex) | ConvertTo-Json -Compress`,
		excl,
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("winnet: ListActiveEgressIfIndexes: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var many []uint32
	if err := json.Unmarshal([]byte(raw), &many); err != nil {
		var one uint32
		if err2 := json.Unmarshal([]byte(raw), &one); err2 != nil {
			return nil, fmt.Errorf("winnet: parse ifIndex list: %v (%s)", err, raw)
		}
		return []uint32{one}, nil
	}
	return many, nil
}

// CaptureAllEgressIPv6 captures ms_tcpip6 state on every active non-Nyxveil egress IF.
// Fail-closed: any interface that cannot be evaluated returns an error (no silent IPv6 leak).
func CaptureAllEgressIPv6(excludeAlias string) ([]IPv6State, error) {
	idxs, err := ListActiveEgressIfIndexes(excludeAlias)
	if err != nil {
		return nil, err
	}
	var out []IPv6State
	for _, idx := range idxs {
		st, err := CaptureIPv6State(idx)
		if err != nil {
			return nil, fmt.Errorf("winnet: IPv6 capture ifIndex %d: %w", idx, err)
		}
		if !st.Captured {
			return nil, fmt.Errorf("winnet: IPv6 state not captured for ifIndex %d", idx)
		}
		out = append(out, st)
	}
	return out, nil
}


// SetIPv6Enabled enables or disables the ms_tcpip6 binding via PowerShell cmdlets
// (locale-independent; no netsh human text).
func SetIPv6Enabled(ifIndex uint32, enabled bool) error {
	if ifIndex == 0 {
		return nil
	}
	cmdlet := "Disable-NetAdapterBinding"
	if enabled {
		cmdlet = "Enable-NetAdapterBinding"
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; %s -InterfaceIndex %d -ComponentID ms_tcpip6 -ErrorAction Stop`,
		cmdlet, ifIndex,
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("winnet: %s ms_tcpip6: %w (%s)", cmdlet, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestoreIPv6State restores exact captured enablement (no permanent global registry edits).
func RestoreIPv6State(st IPv6State) error {
	if !st.Captured || st.InterfaceIndex == 0 {
		return nil
	}
	return SetIPv6Enabled(st.InterfaceIndex, st.Enabled)
}

// DNSServersOnInterface returns IPv4 DNS servers configured on an interface (structured).
func DNSServersOnInterface(ifIndex uint32) ([]string, error) {
	if ifIndex == 0 {
		return nil, fmt.Errorf("winnet: ifIndex required")
	}
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; `+
			`(Get-DnsClientServerAddress -InterfaceIndex %d -AddressFamily IPv4 -ErrorAction Stop).ServerAddresses | `+
			`ConvertTo-Json -Compress`,
		ifIndex,
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("winnet: Get-DnsClientServerAddress: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var many []string
	if err := json.Unmarshal([]byte(raw), &many); err != nil {
		var one string
		if err2 := json.Unmarshal([]byte(raw), &one); err2 != nil {
			// bare string without quotes from ConvertTo-Json single value
			if !strings.HasPrefix(raw, "[") && !strings.HasPrefix(raw, "{") {
				return []string{strings.Trim(raw, `"`)}, nil
			}
			return nil, fmt.Errorf("winnet: parse DNS JSON: %v (%s)", err, raw)
		}
		return []string{one}, nil
	}
	return many, nil
}

// InterfaceIndexByAlias resolves adapter index by name (e.g. Wintun alias).
func InterfaceIndexByAlias(alias string) (uint32, error) {
	script := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; (Get-NetAdapter -Name '%s' -ErrorAction Stop).ifIndex`,
		strings.ReplaceAll(alias, "'", "''"),
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("winnet: Get-NetAdapter: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("winnet: parse ifIndex: %w (%s)", err, string(out))
	}
	return uint32(n), nil
}
