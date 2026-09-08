package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nyxveil/server/internal/paths"
)

const linuxBootIDPath = "/proc/sys/kernel/random/boot_id"

var (
	bootIDReader     = readBootIDFromOS
	bootIDFallbackMu sync.Mutex
	bootIDFallback   string
)

func readBootID() string {
	return bootIDReader()
}

func readBootIDFromOS() string {
	if data, err := os.ReadFile(linuxBootIDPath); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return id
		}
	}
	return readBootIDFallback(paths.StateDir)
}

func readBootIDFallback(stateDir string) string {
	bootIDFallbackMu.Lock()
	defer bootIDFallbackMu.Unlock()
	if bootIDFallback != "" {
		return bootIDFallback
	}
	path := filepath.Join(stateDir, "boot-id-fallback")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			bootIDFallback = id
			return id
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	id := hex.EncodeToString(buf)
	_ = os.MkdirAll(stateDir, 0o700)
	_ = os.WriteFile(path, []byte(id+"\n"), 0o600)
	bootIDFallback = id
	return id
}
