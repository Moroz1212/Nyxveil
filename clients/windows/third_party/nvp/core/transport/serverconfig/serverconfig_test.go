package serverconfig_test

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/nyxveil/nvp/core/internal/testutil"
	"github.com/nyxveil/nvp/core/transport/ech"
	"github.com/nyxveil/nvp/core/transport/serverconfig"
)

func TestTLSServerConfigAppliesECHKeys(t *testing.T) {
	bundle, err := testutil.GenerateCertBundle("localhost")
	if err != nil {
		t.Fatal(err)
	}
	gen, err := ech.GenerateKey("public.localhost", 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg := serverconfig.TLSServerConfig{
		Cert:    bundle.Cert,
		ECHKeys: ech.NewKeySet([]tls.EncryptedClientHelloKey{gen.Key}),
	}.TLSConfig()
	if len(cfg.EncryptedClientHelloKeys) != 1 {
		t.Fatalf("expected ApplyServerKeys to install ECH keys, got %d", len(cfg.EncryptedClientHelloKeys))
	}
	ln, err := serverconfig.NewTLSListener(context.Background(), "127.0.0.1:0", serverconfig.TLSServerConfig{
		Cert:    bundle.Cert,
		ECHKeys: ech.NewKeySet([]tls.EncryptedClientHelloKey{gen.Key}),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
}
