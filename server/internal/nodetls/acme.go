package nodetls

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme"
)

// ACMEConfig drives Let's Encrypt (or compatible) issuance for a VPN node FQDN.
type ACMEConfig struct {
	Domain     string // public DNS name; A/AAAA must point at this host
	Email      string // optional registration contact
	Directory  string // empty = Let's Encrypt production
	HTTPAddr   string // default ":80" for HTTP-01
	AccountKey string // path to ACME account private key
	StateDir   string // challenge / cache dir
	Dest       Paths  // leaf cert + key output
	Replace    bool   // overwrite existing operator certs
}

// IssueOrRenew obtains or renews a publicly trusted certificate.
// Reuses Dest.KeyFile when it holds an ACME-compatible ECDSA key so SPKI stays
// stable across renewals. Incompatible keys (e.g. Ed25519) are replaced with a
// new ECDSA P-256 key at Dest.KeyFile (callers must stage away from live paths).
// Never logs private keys. Returns (cert, previousPin, newPin, pinChanged, err).
func IssueOrRenew(ctx context.Context, cfg ACMEConfig) (cert tls.Certificate, prevPin, newPin []byte, pinChanged bool, err error) {
	if cfg.Domain == "" {
		return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: ACME domain required")
	}
	if cfg.Directory == "" {
		cfg.Directory = acme.LetsEncryptURL
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":80"
	}
	if cfg.AccountKey == "" {
		cfg.AccountKey = filepath.Join(cfg.StateDir, "acme-account.key")
	}
	if Exists(cfg.Dest) && !cfg.Replace {
		existing, loadErr := Load(cfg.Dest)
		if loadErr != nil {
			return tls.Certificate{}, nil, nil, false, loadErr
		}
		pin, pinErr := SPKIPinSHA256(existing)
		if pinErr != nil {
			return tls.Certificate{}, nil, nil, false, pinErr
		}
		leaf, leafErr := x509.ParseCertificate(existing.Certificate[0])
		if leafErr != nil {
			return tls.Certificate{}, nil, nil, false, leafErr
		}
		// Only skip issuance when the existing leaf is still fresh AND covers the
		// requested domain. A valid self-signed IP cert must not short-circuit ACME
		// for a new FQDN (existing-node TLS transition).
		if time.Until(leaf.NotAfter) > 30*24*time.Hour && leaf.VerifyHostname(cfg.Domain) == nil {
			return existing, pin, pin, false, nil
		}
		prevPin = pin
	}

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}

	accountKey, err := loadOrCreateAccountKey(cfg.AccountKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	client := &acme.Client{Key: accountKey, DirectoryURL: cfg.Directory}

	leafKey, err := LoadOrCreateStableKey(cfg.Dest.KeyFile)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}

	if Exists(cfg.Dest) {
		if c, e := Load(cfg.Dest); e == nil {
			if p, e2 := SPKIPinSHA256(c); e2 == nil {
				prevPin = p
			}
		}
	}

	acct := &acme.Account{}
	if cfg.Email != "" {
		acct.Contact = []string{"mailto:" + cfg.Email}
	}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil && !isAlreadyRegistered(err) {
		return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: ACME register: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(cfg.Domain))
	if err != nil {
		return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: ACME order: %w", err)
	}

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: HTTP-01 listen %s: %w (ensure port 80 is free and DNS points here)", cfg.HTTPAddr, err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return tls.Certificate{}, nil, nil, false, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		chal := http01Challenge(authz)
		if chal == nil {
			return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: no HTTP-01 challenge for %s", cfg.Domain)
		}
		resp, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			return tls.Certificate{}, nil, nil, false, err
		}
		path := client.HTTP01ChallengePath(chal.Token)
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(resp))
		})
		if _, err := client.Accept(ctx, chal); err != nil {
			return tls.Certificate{}, nil, nil, false, err
		}
		if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
			return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: ACME authorization: %w", err)
		}
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: cfg.Domain},
		DNSNames: []string{cfg.Domain},
	}, leafKey)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}

	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, fmt.Errorf("nodetls: ACME finalize: %w", err)
	}
	if err := WriteLeafChain(cfg.Dest, der, leafKey); err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	cert, err = Load(cfg.Dest)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	newPin, err = SPKIPinSHA256(cert)
	if err != nil {
		return tls.Certificate{}, nil, nil, false, err
	}
	pinChanged = PinChanged(prevPin, newPin)
	if pinChanged && len(prevPin) > 0 {
		log.Printf("nodetls: SPKI pin changed after ACME renew — re-register node so Control Plane catalog pin stays consistent")
	}
	log.Printf("nodetls: ACME certificate ready for %s (pin_changed=%v)", cfg.Domain, pinChanged)
	return cert, prevPin, newPin, pinChanged, nil
}

func http01Challenge(a *acme.Authorization) *acme.Challenge {
	for _, c := range a.Challenges {
		if c.Type == "http-01" {
			return c
		}
	}
	return nil
}

func isAlreadyRegistered(err error) bool {
	ae, ok := err.(*acme.Error)
	return ok && (ae.StatusCode == http.StatusConflict || ae.StatusCode == 409)
}

func loadOrCreateAccountKey(path string) (crypto.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("nodetls: bad ACME account key PEM")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			ec, err2 := x509.ParseECPrivateKey(block.Bytes)
			if err2 != nil {
				return nil, err
			}
			return ec, nil
		}
		ec, ok := k.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("nodetls: ACME account key must be ECDSA")
		}
		return ec, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return priv, atomicWrite(path+".tmp", path, pemBytes, 0o600)
}
