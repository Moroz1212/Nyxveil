package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/controlplane/server"
	"github.com/nyxveil/nvp/core/controlplane/store"
)

func main() {
	addr := flag.String("listen", ":8443", "listen address")
	dbPath := flag.String("db", "nvp-controlplane.db", "sqlite database path")
	keysDir := flag.String("keys", "keys", "directory for persistent signing keys")
	production := flag.Bool("production", true, "use production server with SQLite")
	tlsCert := flag.String("cert", "", "TLS certificate file (required for production HTTPS)")
	tlsKey := flag.String("key", "", "TLS private key file")
	issuerURL := flag.String("issuer", "https://control.nyxveil.local", "JWT issuer URL")
	allowInsecureDev := flag.Bool("allow-insecure-dev", false,
		"dev only: allow HTTP on localhost and empty NVP_LICENSE_KEK; never use in production")
	flag.Parse()

	keyMat, err := server.LoadOrGenerateKeys(*keysDir)
	if err != nil {
		log.Fatal(err)
	}

	issuer := keyMat.BuildIssuerConfig(*issuerURL, "nvp-node")
	issuer.KeyID = keyMat.IssuerKeyID
	issuer.PrivateKey = keyMat.IssuerPriv
	issuer.TTL = 15 * time.Minute

	cfg := server.Config{
		Issuer:        issuer,
		CatalogSigner: keyMat.CatalogSigner,
		Catalog: model.Catalog{
			Version: "1",
			Locations: []model.Location{
				{LocationID: "fi-hel", Country: "FI", City: "Helsinki", DisplayName: "Finland", Enabled: true},
			},
		},
		Options: server.ServerOptions{
			TLS:              server.TLSConfig{CertFile: *tlsCert, KeyFile: *tlsKey},
			RateLimit:        server.DefaultRateLimit(),
			AllowInsecureDev: *allowInsecureDev,
		},
	}

	if *production && !*allowInsecureDev {
		if err := server.RequireProduction(cfg.Options); err != nil {
			log.Fatal(err)
		}
	} else if msg := server.WarnIfInsecure(cfg.Options); msg != "" {
		log.Println(msg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *production {
		var st *store.Store
		var err error
		if *allowInsecureDev {
			st, err = store.Open(*dbPath)
		} else {
			st, err = store.OpenProduction(*dbPath)
		}
		if err != nil {
			log.Fatal(err)
		}
		defer st.Close()

		srv := server.NewProduction(cfg, st)
		go func() {
			log.Printf("NVP Control Plane PRODUCTION listening on %s db=%s keys=%s", *addr, *dbPath, *keysDir)
			if err := srv.ListenAndServe(*addr); err != nil {
				log.Printf("server stopped: %v", err)
			}
		}()
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	} else {
		stub := server.NewStub(cfg)
		if *allowInsecureDev {
			stub.RegisterLicense(model.LicenseRecord{
				LicenseID:  "nyx_lic_dev",
				Plan:       "premium",
				MaxDevices: 3,
				Enabled:    true,
				ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
				Locations:  []string{"fi-hel"},
				Secret:     "dev-only-secret",
			})
			log.Println("WARNING: stub registered example license under -allow-insecure-dev")
		}
		go func() {
			log.Printf("NVP Control Plane STUB listening on %s", *addr)
			if err := stub.ListenAndServe(*addr); err != nil {
				log.Printf("server stopped: %v", err)
			}
		}()
		<-ctx.Done()
		_ = stub.Shutdown(context.Background())
	}
	log.Println("shutdown complete")
}
