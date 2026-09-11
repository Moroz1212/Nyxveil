package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
	"github.com/nyxveil/server/internal/localconfig"
	"github.com/nyxveil/server/internal/paths"
)

// Validate material even when lifecycle deliberately keeps listeners stopped.
func verifyUpdateTLS() error {
	cfg, err := localconfig.Load(paths.ServerConfig())
	if err != nil {
		return fmt.Errorf("TLS configuration: %w", err)
	}
	cert, key := cfg.TLSCertFile, cfg.TLSKeyFile
	if cert == "" {
		cert = filepath.Join(runtimeStateDir(), "tls.crt")
	}
	if key == "" {
		key = filepath.Join(runtimeStateDir(), "tls.key")
	}
	uid, gid, err := filemeta.LookupServiceIDs()
	if err != nil {
		return fmt.Errorf("TLS service identity unavailable: %w", err)
	}
	for _, f := range []struct {
		path string
		mode uint32
	}{{cert, 0644}, {key, 0600}, {runtimeStateDir(), 0700}} {
		m, err := filemeta.CaptureMeta(f.path)
		if err != nil {
			return fmt.Errorf("TLS metadata: %w", err)
		}
		if !m.Exists || m.IsSymlink || uint32(m.Mode.Perm()) != f.mode || m.UID != uid || m.GID != gid {
			return fmt.Errorf("TLS ownership/mode invalid: %s", filepath.Base(f.path))
		}
	}
	name := cfg.ServerName
	if name == "" {
		name = cfg.PublicHost
	}
	return validateTLSMaterial(cert, key, name)
}

func validateTLSMaterial(cert, key, name string) error {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return fmt.Errorf("TLS certificate/key integrity invalid")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("TLS certificate invalid")
	}
	if time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return fmt.Errorf("TLS certificate outside validity period")
	}
	if name == "" {
		return fmt.Errorf("TLS server_name missing")
	}
	if err := leaf.VerifyHostname(name); err != nil {
		return fmt.Errorf("TLS SAN does not match configured server_name")
	}
	return nil
}
