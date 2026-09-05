//go:build ignore

// Command sign-release builds and signs release-manifest-linux-{amd64,arm64}.json
// matching internal/updater.CanonicalManifestBytes / ParseManifest.
//
// Private key (64-byte ed25519.PrivateKey = seed||pub, or 32-byte seed):
//
//	NYXVEIL_RELEASE_SIGNING_KEY  — base64 (std or raw-url) of key bytes
//	or file .secrets/release-signing.ed25519 (raw 32 or 64 bytes, or base64 text)
//
// Usage:
//
//	go run ./scripts/sign-release.go \
//	  -version 1.0.0 -out dist/release \
//	  -amd64-server path -amd64-ctl path -arm64-server path -arm64-ctl path \
//	  [-base-url https://github.com/org/repo/releases/download/server-v1.0.0]
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nyxveil/server/internal/updater"
)

func main() {
	version := flag.String("version", "", "release version (required)")
	outDir := flag.String("out", "dist/release", "output directory")
	baseURL := flag.String("base-url", "", "asset URL prefix (default GitHub release tag URL)")
	minCore := flag.String("min-core", "1.0.0", "min_core field")
	minProto := flag.Uint("min-protocol", 1, "min_protocol field")
	amd64Server := flag.String("amd64-server", "", "path to nyxveil-server-linux-amd64")
	amd64Ctl := flag.String("amd64-ctl", "", "path to nyxveilctl-linux-amd64")
	arm64Server := flag.String("arm64-server", "", "path to nyxveil-server-linux-arm64")
	arm64Ctl := flag.String("arm64-ctl", "", "path to nyxveilctl-linux-arm64")
	flag.Parse()

	if strings.TrimSpace(*version) == "" {
		fatal(" -version is required")
	}
	priv, err := loadPrivateKey()
	if err != nil {
		fatal("%v", err)
	}

	if *baseURL == "" {
		*baseURL = fmt.Sprintf("https://github.com/Moroz1212/Nyxveil/releases/download/server-v%s", *version)
	}
	*baseURL = strings.TrimRight(*baseURL, "/")

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("%v", err)
	}

	type archSpec struct {
		goArch string
		server string
		ctl    string
	}
	specs := []archSpec{
		{"amd64", *amd64Server, *amd64Ctl},
		{"arm64", *arm64Server, *arm64Ctl},
	}

	for _, s := range specs {
		if s.server == "" || s.ctl == "" {
			fmt.Fprintf(os.Stderr, "sign-release: skip linux/%s (paths not set)\n", s.goArch)
			continue
		}
		if err := writeManifest(*outDir, *version, s.goArch, *baseURL, *minCore, uint16(*minProto), s.server, s.ctl, priv); err != nil {
			fatal("linux/%s: %v", s.goArch, err)
		}
	}
}

func writeManifest(outDir, version, goArch, baseURL, minCore string, minProto uint16, serverPath, ctlPath string, priv ed25519.PrivateKey) error {
	serverSum, err := fileSHA256(serverPath)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	ctlSum, err := fileSHA256(ctlPath)
	if err != nil {
		return fmt.Errorf("ctl: %w", err)
	}

	serverName := fmt.Sprintf("nyxveil-server-linux-%s", goArch)
	ctlName := fmt.Sprintf("nyxveilctl-linux-%s", goArch)

	m := &updater.Manifest{
		Version:     version,
		Arch:        "linux/" + goArch,
		MinCore:     minCore,
		MinProtocol: minProto,
		Assets: []updater.Asset{
			{Name: "nyxveil-server", SHA256: serverSum, URL: baseURL + "/" + serverName},
			{Name: "nyxveilctl", SHA256: ctlSum, URL: baseURL + "/" + ctlName},
		},
	}
	updater.SignManifest(m, priv)

	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	out := filepath.Join(outDir, fmt.Sprintf("release-manifest-linux-%s.json", goArch))
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (sig ok)\n", out)

	// Sanity: ParseManifest must accept what we wrote.
	if _, err := updater.ParseManifest(raw, updater.UpdatePublicKey); err != nil {
		return fmt.Errorf("self-verify failed (is signing key paired with UpdatePublicKey?): %w", err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func loadPrivateKey() (ed25519.PrivateKey, error) {
	if env := strings.TrimSpace(os.Getenv("NYXVEIL_RELEASE_SIGNING_KEY")); env != "" {
		return parseKeyBytes([]byte(env), true)
	}
	path := filepath.Join(".secrets", "release-signing.ed25519")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("set NYXVEIL_RELEASE_SIGNING_KEY or create %s: %w", path, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseKeyBytes(b, true)
}

func parseKeyBytes(b []byte, allowB64 bool) (ed25519.PrivateKey, error) {
	b = bytesTrim(b)
	if allowB64 {
		if decoded, err := base64.StdEncoding.DecodeString(string(b)); err == nil {
			b = decoded
		} else if decoded, err := base64.RawURLEncoding.DecodeString(string(b)); err == nil {
			b = decoded
		} else if decoded, err := base64.RawStdEncoding.DecodeString(string(b)); err == nil {
			b = decoded
		}
	}
	switch len(b) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	default:
		return nil, fmt.Errorf("signing key must be 32-byte seed or 64-byte private key (got %d bytes)", len(b))
	}
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sign-release: "+format+"\n", args...)
	os.Exit(1)
}
