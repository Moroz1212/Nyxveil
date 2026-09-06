//go:build ignore

// Command verify-manifest-hashes checks release-manifest-linux-*.json asset
// SHA-256 values against flat files in -dist.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyxveil/server/internal/updater"
)

func main() {
	dist := flag.String("dist", "dist/release", "release directory")
	version := flag.String("version", "", "expected version (optional)")
	flag.Parse()

	for _, arch := range []string{"amd64", "arm64"} {
		manPath := filepath.Join(*dist, "release-manifest-linux-"+arch+".json")
		raw, err := os.ReadFile(manPath)
		if err != nil {
			fatal("%v", err)
		}
		m, err := updater.ParseManifest(raw, updater.UpdatePublicKey)
		if err != nil {
			fatal("%s: %v", manPath, err)
		}
		if m.Arch != "linux/"+arch {
			fatal("%s: arch %q", manPath, m.Arch)
		}
		if *version != "" && m.Version != *version {
			fatal("%s: version %q want %q", manPath, m.Version, *version)
		}
		seen := map[string]bool{}
		for _, a := range m.Assets {
			path, err := resolveAssetPath(*dist, arch, a.Name)
			if err != nil {
				fatal("%v", err)
			}
			sum, err := fileSHA(path)
			if err != nil {
				fatal("%v", err)
			}
			if !strings.EqualFold(sum, a.SHA256) {
				fatal("%s %s: manifest=%s file=%s", arch, a.Name, a.SHA256, sum)
			}
			seen[a.Name] = true
		}
		for _, name := range updater.RequiredAssetNames {
			if !seen[name] {
				fatal("%s: missing required asset %q", arch, name)
			}
		}
		fmt.Printf("ok %s\n", arch)
	}
}

func resolveAssetPath(dist, arch, name string) (string, error) {
	switch name {
	case "nyxveil-server", "server":
		return filepath.Join(dist, "nyxveil-server-linux-"+arch), nil
	case "nyxveilctl":
		return filepath.Join(dist, "nyxveilctl-linux-"+arch), nil
	case "nyxveil-catalog-verify":
		return filepath.Join(dist, "nyxveil-catalog-verify-linux-"+arch), nil
	case "production-gate":
		return filepath.Join(dist, "production-gate.sh"), nil
	case "share-version":
		return filepath.Join(dist, "VERSION"), nil
	case "share-third-party-core":
		return filepath.Join(dist, "THIRD_PARTY_CORE.md"), nil
	default:
		return "", fmt.Errorf("unknown asset %q", name)
	}
}

func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "verify-manifest-hashes: "+format+"\n", args...)
	os.Exit(1)
}
