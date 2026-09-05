//go:build ignore

// Command verify-manifest-hashes checks release-manifest-linux-*.json asset
// SHA-256 values against flat binaries in -dist.
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
		for _, a := range m.Assets {
			var path string
			switch a.Name {
			case "nyxveil-server", "server":
				path = filepath.Join(*dist, "nyxveil-server-linux-"+arch)
			case "nyxveilctl":
				path = filepath.Join(*dist, "nyxveilctl-linux-"+arch)
			default:
				fatal("unknown asset %q", a.Name)
			}
			sum, err := fileSHA(path)
			if err != nil {
				fatal("%v", err)
			}
			if !strings.EqualFold(sum, a.SHA256) {
				fatal("%s %s: manifest=%s file=%s", arch, a.Name, a.SHA256, sum)
			}
		}
		fmt.Printf("ok %s\n", arch)
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
