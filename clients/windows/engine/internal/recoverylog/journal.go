package recoverylog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Mutation is one reversible OS change with exact undo parameters.
type Mutation struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // bypass_route|default_vpn|tun_addr|tun_dns|ipv6_set

	// Route fields (IP Helper)
	DestPrefix string `json:"dest_prefix,omitempty"`
	NextHop    string `json:"next_hop,omitempty"`
	IfIndex    uint32 `json:"if_index,omitempty"`
	IfLUID     uint64 `json:"if_luid,omitempty"`
	Metric     uint32 `json:"metric,omitempty"`

	// TUN / DNS
	TunName string   `json:"tun_name,omitempty"`
	DNS     []string `json:"dns,omitempty"`

	// IPv6 exact prior state
	IPv6IfIndex     uint32 `json:"ipv6_if_index,omitempty"`
	IPv6WasEnabled  *bool  `json:"ipv6_was_enabled,omitempty"`
	IPv6NowEnabled  *bool  `json:"ipv6_now_enabled,omitempty"`
}

// Journal is crash-safe: rewritten atomically before and after each mutation.
type Journal struct {
	SavedAt            time.Time  `json:"saved_at"`
	Phase              string     `json:"phase"`
	Pending            *Mutation  `json:"pending,omitempty"` // set BEFORE OS call
	Applied            []Mutation `json:"applied"`
	OriginalDefault    *Mutation  `json:"original_default,omitempty"`
	OriginalIPv6Phys   *Mutation  `json:"original_ipv6_phys,omitempty"` // legacy first IF
	OriginalIPv6       []Mutation `json:"original_ipv6,omitempty"`      // all egress IFs
}

func DefaultPath() string {
	base := os.Getenv("PROGRAMDATA")
	if base == "" {
		base = filepath.Join(os.TempDir(), "Nyxveil")
	}
	return filepath.Join(base, "Nyxveil", "Client", "route-journal.json")
}

func Write(path string, j Journal) error {
	if err := ensureJournalDir(path); err != nil {
		return err
	}
	if err := assertSafeJournalPaths(path); err != nil {
		return err
	}
	j.SavedAt = time.Now().UTC()
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := rejectReparse(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func Read(path string) (*Journal, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func Clear(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
