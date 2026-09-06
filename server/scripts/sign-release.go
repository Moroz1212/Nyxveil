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
//	  -version 1.1.2 -out dist/release \
//	  -amd64-server path -amd64-ctl path -amd64-catalog path \
//	  -arm64-server path -arm64-ctl path -arm64-catalog path \
//	  -production-gate path -share-version path -share-third-party path \
//	  [-base-url https://github.com/org/repo/releases/download/server-v1.1.2]
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

	"github.com/nyxveil/server/internal/paths"
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
	amd64Catalog := flag.String("amd64-catalog", "", "path to nyxveil-catalog-verify-linux-amd64")
	arm64Server := flag.String("arm64-server", "", "path to nyxveil-server-linux-arm64")
	arm64Ctl := flag.String("arm64-ctl", "", "path to nyxveilctl-linux-arm64")
	arm64Catalog := flag.String("arm64-catalog", "", "path to nyxveil-catalog-verify-linux-arm64")
	productionGate := flag.String("production-gate", "", "path to production-gate.sh")
	shareVersion := flag.String("share-version", "", "path to VERSION share file")
	shareThirdParty := flag.String("share-third-party", "", "path to THIRD_PARTY_CORE.md")
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
		goArch  string
		server  string
		ctl     string
		catalog string
	}
	specs := []archSpec{
		{"amd64", *amd64Server, *amd64Ctl, *amd64Catalog},
		{"arm64", *arm64Server, *arm64Ctl, *arm64Catalog},
	}

	for _, s := range specs {
		if s.server == "" || s.ctl == "" || s.catalog == "" {
			fmt.Fprintf(os.Stderr, "sign-release: skip linux/%s (binary paths not set)\n", s.goArch)
			continue
		}
		if *productionGate == "" || *shareVersion == "" || *shareThirdParty == "" {
			fatal("production-gate, share-version, and share-third-party are required")
		}
		if err := writeManifest(*outDir, *version, s.goArch, *baseURL, *minCore, uint16(*minProto),
			s.server, s.ctl, s.catalog, *productionGate, *shareVersion, *shareThirdParty, priv); err != nil {
			fatal("linux/%s: %v", s.goArch, err)
		}
	}
}

func writeManifest(outDir, version, goArch, baseURL, minCore string, minProto uint16,
	serverPath, ctlPath, catalogPath, gatePath, versionPath, thirdPartyPath string, priv ed25519.PrivateKey) error {
	type namedPath struct {
		name        string
		path        string
		url         string
		destination string
		mode        string
	}
	items := []namedPath{
		{"nyxveil-server", serverPath, baseURL + "/" + fmt.Sprintf("nyxveil-server-linux-%s", goArch), paths.BinaryPath(), "0755"},
		{"nyxveilctl", ctlPath, baseURL + "/" + fmt.Sprintf("nyxveilctl-linux-%s", goArch), paths.BinDir + "/nyxveilctl", "0755"},
		{"nyxveil-catalog-verify", catalogPath, baseURL + "/" + fmt.Sprintf("nyxveil-catalog-verify-linux-%s", goArch), paths.CatalogVerify(), "0755"},
		{"production-gate", gatePath, baseURL + "/production-gate.sh", paths.ProductionGate(), "0755"},
		{"share-version", versionPath, baseURL + "/VERSION", paths.ShareVersion(), "0644"},
		{"share-third-party-core", thirdPartyPath, baseURL + "/THIRD_PARTY_CORE.md", paths.ShareThirdParty(), "0644"},
	}

	m := &updater.Manifest{
		Version:     version,
		Arch:        "linux/" + goArch,
		MinCore:     minCore,
		MinProtocol: minProto,
	}
	for _, item := range items {
		sum, err := fileSHA256(item.path)
		if err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
		m.Assets = append(m.Assets, updater.Asset{
			Name: item.name, SHA256: sum, URL: item.url, Destination: item.destination,
			Mode: item.mode, Required: true,
		})
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
	fmt.Printf("wrote %s (sig ok, assets=%d)\n", out, len(m.Assets))

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
