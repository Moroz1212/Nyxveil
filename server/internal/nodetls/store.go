// Package nodetls manages VPN node leaf certificates for client-facing TLS/QUIC.
//
// Trust model (Frozen Core unchanged):
//   clients use normal x509 validation (SystemTrust or explicit RootCAs) THEN SPKI pin.
//   Self-signed leaves fail SystemTrust even with a correct catalog pin.
//
// Operator-provided files (tls_cert_file / tls_key_file) are never overwritten by
// ACME or self-signed generation unless ReplaceExisting is set.
package nodetls

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nyxveil/server/internal/filemeta"
)

// Paths holds on-disk certificate material.
type Paths struct {
	CertFile string // full chain PEM (leaf first)
	KeyFile  string // PKCS#8 private key PEM (0600)
}

// SPKIPinSHA256 returns SHA-256 of the leaf certificate SPKI.
func SPKIPinSHA256(cert tls.Certificate) ([]byte, error) {
	if len(cert.Certificate) == 0 {
		return nil, errors.New("nodetls: empty certificate")
	}
	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return sum[:], nil
}

// Load loads an existing key pair. Does not create files.
func Load(p Paths) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
}

// Exists reports whether both cert and key files are present.
func Exists(p Paths) bool {
	_, errC := os.Stat(p.CertFile)
	_, errK := os.Stat(p.KeyFile)
	return errC == nil && errK == nil
}

// InstallOperatorCopies operator-provided cert/key into dest without overwriting
// unless replace is true. Permissions: cert 0644, key 0600.
func InstallOperator(srcCert, srcKey string, dest Paths, replace bool) error {
	if srcCert == "" || srcKey == "" {
		return errors.New("nodetls: operator cert and key paths required")
	}
	if Exists(dest) && !replace {
		// Verify readable; keep existing material (stable SPKI).
		if _, err := Load(dest); err != nil {
			return fmt.Errorf("nodetls: existing TLS material unreadable: %w", err)
		}
		return nil
	}
	certPEM, err := os.ReadFile(srcCert)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(srcKey)
	if err != nil {
		return err
	}
	// Validate pair before writing.
	tmpCert := dest.CertFile + ".op.tmp"
	tmpKey := dest.KeyFile + ".op.tmp"
	if err := os.MkdirAll(filepath.Dir(dest.CertFile), 0o700); err != nil {
		return err
	}
	if err := atomicWrite(tmpCert, dest.CertFile, certPEM, 0o644); err != nil {
		return err
	}
	if err := atomicWrite(tmpKey, dest.KeyFile, keyPEM, 0o600); err != nil {
		return err
	}
	if _, err := Load(dest); err != nil {
		return fmt.Errorf("nodetls: operator material invalid: %w", err)
	}
	return nil
}

// ACMECompatibleLeafKey reports whether signer can be reused for WebPKI ACME CSRs.
// Compatible: ECDSA (any NIST curve already used by the leaf). Incompatible: Ed25519, RSA-only
// paths we do not prefer for new issuance, and unknown types.
func ACMECompatibleLeafKey(signer crypto.Signer) bool {
	if signer == nil {
		return false
	}
	switch signer.(type) {
	case *ecdsa.PrivateKey:
		return true
	default:
		return false
	}
}

// ParseLeafPrivateKeyPEM parses a PKCS#8 / EC private key PEM. Never logs key material.
func ParseLeafPrivateKeyPEM(pemBytes []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("nodetls: invalid key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		ec, err2 := x509.ParseECPrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("nodetls: parse key: %v / %v", err, err2)
		}
		return ec, nil
	}
	switch k := key.(type) {
	case *ecdsa.PrivateKey:
		return k, nil
	case ed25519.PrivateKey:
		return k, nil
	case *rsa.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("nodetls: unsupported private key type %T", key)
	}
}

// KeyFileACMECompatible reports whether keyFile exists and holds an ACME-reusable leaf key.
func KeyFileACMECompatible(keyFile string) bool {
	b, err := os.ReadFile(keyFile)
	if err != nil {
		return false
	}
	signer, err := ParseLeafPrivateKeyPEM(b)
	if err != nil {
		return false
	}
	return ACMECompatibleLeafKey(signer)
}

// LoadOrCreateStableKey loads an existing ACME-compatible private key or creates a new
// ECDSA P-256 key at keyFile (0600).
//
// "Reuse when possible" means: reuse ECDSA keys; if the on-disk key is incompatible
// (e.g. Ed25519 from legacy self-signed nodes), replace it with a new P-256 key.
// Callers that stage ACME material must point Dest.KeyFile at staging so live keys stay untouched.
func LoadOrCreateStableKey(keyFile string) (crypto.Signer, error) {
	if b, err := os.ReadFile(keyFile); err == nil {
		signer, perr := ParseLeafPrivateKeyPEM(b)
		if perr == nil && ACMECompatibleLeafKey(signer) {
			return signer, nil
		}
		// Incompatible or unreadable for ACME — fall through and generate P-256.
		// Do not log key material; type string is operator-safe.
		if perr == nil {
			log.Printf("nodetls: existing leaf key type %T is not ACME/WebPKI compatible; generating new ECDSA P-256 key", signer)
		} else {
			log.Printf("nodetls: existing leaf key unusable for ACME (%v); generating new ECDSA P-256 key", perr)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return createAndPersistP256Key(keyFile)
}

func createAndPersistP256Key(keyFile string) (crypto.Signer, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, err
	}
	if err := atomicWrite(keyFile+".tmp", keyFile, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// WriteLeafChain writes cert chain + private key atomically. Never logs key material.
func WriteLeafChain(dest Paths, certDERs [][]byte, key crypto.Signer) error {
	if len(certDERs) == 0 {
		return errors.New("nodetls: empty cert chain")
	}
	var certPEM []byte
	for _, der := range certDERs {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(filepath.Dir(dest.CertFile), 0o700); err != nil {
		return err
	}
	tmpC := dest.CertFile + ".tmp"
	tmpK := dest.KeyFile + ".tmp"
	if err := atomicWrite(tmpC, dest.CertFile, certPEM, 0o644); err != nil {
		return err
	}
	if err := atomicWrite(tmpK, dest.KeyFile, keyPEM, 0o600); err != nil {
		return err
	}
	_ = filemeta.EnforceRuntimeTLS(filepath.Dir(dest.KeyFile))
	return nil
}

func atomicWrite(tmp, final string, data []byte, mode os.FileMode) error {
	_ = tmp
	uid, gid := -1, -1
	if prev, err := filemeta.CaptureMeta(final); err == nil && prev.Exists {
		uid, gid = prev.UID, prev.GID
	}
	if err := filemeta.AtomicWrite(final, data, mode, uid, gid); err != nil {
		return err
	}
	base := filepath.Base(final)
	if base == "tls.key" || base == "tls.crt" {
		_ = filemeta.EnforceRuntimeTLS(filepath.Dir(final))
	}
	return nil
}

// GenerateSelfSignedDev writes a self-signed leaf for lab use only.
// Windows SystemTrust clients will reject this even with a correct SPKI pin.
func GenerateSelfSignedDev(dest Paths, serverName string, replace bool) error {
	if Exists(dest) && !replace {
		_, err := Load(dest)
		return err
	}
	key, err := LoadOrCreateStableKey(dest.KeyFile)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: serverName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(serverName); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{serverName}
	}
	pub := key.Public()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		return err
	}
	return WriteLeafChain(dest, [][]byte{der}, key)
}

// PinChanged reports whether two SPKI pins differ.
func PinChanged(a, b []byte) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}
