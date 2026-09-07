package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/node"
	"github.com/nyxveil/nvp/core/transport"
)

func main() {
	cmd := "canon-fixture"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "floats":
		printFloats()
	case "canon-fixture":
		printCanon(mustCanon(canonFixtureCatalog()))
	default:
		printCanon(mustCanon(edgeCatalog(cmd)))
	}
}

func printCanon(b []byte) {
	sum := sha256.Sum256(b)
	fmt.Printf("len=%d sha256=%s\n%s\n", len(b), hex.EncodeToString(sum[:]), string(b))
}

func mustCanon(cat model.Catalog) []byte {
	b, err := canon(cat)
	if err != nil {
		panic(err)
	}
	return b
}

func printFloats() {
	type H struct {
		CPU float64 `json:"cpu_percent"`
		Mem float64 `json:"memory_percent"`
	}
	vals := []H{
		{0.268366320026836, 54.67981109019452},
		{0, 0},
		{0.1, 1.5},
		{1, 1},
		{1e-10, 1e21},
		{math.Copysign(0, -1), -1.5},
	}
	for _, h := range vals {
		b, _ := json.Marshal(h)
		fmt.Println(string(b))
	}
}

func canonFixtureCatalog() model.Catalog {
	issued := time.Date(2026, 9, 7, 10, 0, 0, 123456700, time.UTC)
	expires := issued.Add(time.Hour)
	spki := make([]byte, 32)
	for i := range spki {
		spki[i] = byte(i + 1)
	}
	return model.Catalog{
		Version: "cat_test_fixture_1",
		Locations: []model.Location{{
			LocationID: "fi-helsinki", Country: "Finland", CountryCode: "FI",
			City: "Helsinki", DisplayName: "Helsinki", Enabled: true,
		}},
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "nv-test-227e939e", LocationID: "fi-helsinki",
			Country: "Finland", City: "Helsinki", DisplayName: "fi-hel-01",
			Status: node.StatusHealthy, Enabled: true, ProtocolVersion: 1,
			ServerVersion: "1.1.4",
			Endpoints: []transport.Endpoint{{
				Host: "fi-hel-01.nyxveil.ru", Port: 443,
				Profiles: []transport.Profile{transport.ProfileTLSTCP},
			}},
			ServerName: "fi-hel-01.nyxveil.ru",
			SPKIPin:    spki,
			Capacity:   100, CurrentSessions: 3,
			Health: model.HealthInfo{
				Healthy: true, LatencyMs: 12.5, SessionCount: 3,
				CPUPercent: 0.268366320026836, MemoryPercent: 54.67981109019452,
			},
			LastSeen: issued,
		}},
		IssuedAt:  issued,
		ExpiresAt: expires,
	}
}

func edgeCatalog(name string) model.Catalog {
	issued := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	expires := issued.Add(time.Hour)
	base := model.Catalog{
		Version:   "v1",
		IssuedAt:  issued,
		ExpiresAt: expires,
		Locations: []model.Location{{
			LocationID: "a", Country: "C", CountryCode: "CC", City: "City", DisplayName: "Disp", Enabled: true,
		}},
		Nodes: []model.NodeRegistryEntry{{
			NodeID: "n1", LocationID: "a", Country: "C", City: "City", DisplayName: "Disp",
			Status: node.StatusHealthy, Enabled: true, ProtocolVersion: 1, ServerVersion: "1.0.0",
			Endpoints: []transport.Endpoint{{Host: "h", Port: 443, Profiles: []transport.Profile{"tls-tcp-443"}}},
			Capacity:  1, Health: model.HealthInfo{Healthy: true}, LastSeen: issued,
		}},
	}
	switch name {
	case "negzero":
		base.Nodes[0].Health.CPUPercent = math.Copysign(0, -1)
		base.Nodes[0].Health.MemoryPercent = 1
	case "sci":
		base.Nodes[0].Health.CPUPercent = 1e-10
		base.Nodes[0].Health.MemoryPercent = 1e21
	case "html":
		base.Nodes[0].DisplayName = "A&B<C>"
		base.Locations[0].DisplayName = "X&Y"
	case "empty_server":
		base.Nodes[0].ServerName = ""
	case "empty_spki":
		base.Nodes[0].SPKIPin = []byte{}
	case "nil_spki":
		base.Nodes[0].SPKIPin = nil
	case "server_set":
		base.Nodes[0].ServerName = "fi-hel-01.nyxveil.ru"
	case "frac1":
		base.IssuedAt = time.Date(2026, 9, 7, 10, 0, 0, 100000000, time.UTC)
		base.ExpiresAt = base.IssuedAt.Add(time.Hour)
		base.Nodes[0].LastSeen = base.IssuedAt
	case "frac3":
		base.IssuedAt = time.Date(2026, 9, 7, 10, 0, 0, 123000000, time.UTC)
		base.ExpiresAt = base.IssuedAt.Add(time.Hour)
		base.Nodes[0].LastSeen = base.IssuedAt
	case "frac6":
		base.IssuedAt = time.Date(2026, 9, 7, 10, 0, 0, 123456000, time.UTC)
		base.ExpiresAt = base.IssuedAt.Add(time.Hour)
		base.Nodes[0].LastSeen = base.IssuedAt
	case "trail0":
		base.IssuedAt = time.Date(2026, 9, 7, 10, 0, 0, 120000000, time.UTC)
		base.ExpiresAt = base.IssuedAt.Add(time.Hour)
		base.Nodes[0].LastSeen = base.IssuedAt
	case "unicode":
		base.Nodes[0].DisplayName = "Привет"
		base.Locations[0].City = "Helsinki—metro"
	case "ipfamily":
		base.Nodes[0].Endpoints[0].IPFamily = "dual"
	case "empty_profiles":
		base.Nodes[0].Endpoints[0].Profiles = []transport.Profile{}
	case "nil_profiles":
		base.Nodes[0].Endpoints[0].Profiles = nil
	case "empty_locs":
		base.Locations = nil
		base.Nodes = nil
	case "prod_ip_server":
		base.Nodes[0].ServerName = "46.8.218.27"
		base.Nodes[0].ServerVersion = "1.0.1"
		base.Nodes[0].Endpoints[0].Host = "46.8.218.27"
		base.Nodes[0].Endpoints[0].IPFamily = "ipv4"
	default:
		fmt.Fprintln(os.Stderr, "unknown fixture:", name)
		os.Exit(2)
	}
	return base
}

func canon(cat model.Catalog) ([]byte, error) {
	type c struct {
		Version   string                    `json:"version"`
		Locations []model.Location          `json:"locations"`
		Nodes     []model.NodeRegistryEntry `json:"nodes"`
		IssuedAt  time.Time                 `json:"issued_at"`
		ExpiresAt time.Time                 `json:"expires_at"`
	}
	nodes := append([]model.NodeRegistryEntry(nil), cat.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	locs := append([]model.Location(nil), cat.Locations...)
	sort.Slice(locs, func(i, j int) bool { return locs[i].LocationID < locs[j].LocationID })
	return json.Marshal(c{
		Version: cat.Version, Locations: locs, Nodes: nodes,
		IssuedAt: cat.IssuedAt.UTC(), ExpiresAt: cat.ExpiresAt.UTC(),
	})
}
