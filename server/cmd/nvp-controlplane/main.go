package main

import (
	"context"
	"crypto/ed25519"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/controlplane/catalog"
	"github.com/nyxveil/nvp/controlplane/model"
	"github.com/nyxveil/nvp/controlplane/server"
	"github.com/nyxveil/nvp/controlplane/store"
)

func main() {
	addr := flag.String("listen", ":8443", "listen address")
	dbPath := flag.String("db", "nvp-controlplane.db", "sqlite database path")
	production := flag.Bool("production", true, "use production server with SQLite")
	flag.Parse()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		log.Fatal(err)
	}
	_ = pub

	issuer := ticket.IssuerConfig{
		Issuer:     "https://control.nyxveil.local",
		Audience:   "nvp-node",
		KeyID:      "cp-key-1",
		PrivateKey: priv,
		TTL:        15 * time.Minute,
	}

	cfg := server.Config{
		Issuer:        issuer,
		CatalogSigner: catalog.Signer{KeyID: "cat-key-1", PrivateKey: priv},
		Catalog: model.Catalog{
			Version: "1",
			Locations: []model.Location{
				{LocationID: "fi-hel", Country: "FI", City: "Helsinki", DisplayName: "Finland", Enabled: true},
			},
		},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *production {
		st, err := store.Open(*dbPath)
		if err != nil {
			log.Fatal(err)
		}
		defer st.Close()

		srv := server.NewProduction(cfg, st)
		_ = srv.RegisterLicense(ctx, model.LicenseRecord{
			LicenseID:  "nyx_lic_demo",
			Plan:       "premium",
			MaxDevices: 3,
			Enabled:    true,
			ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
			Locations:  []string{"fi-hel"},
		})
		_ = srv.RegisterNodeIdentity(ctx, "fi-hel-01", priv.Public().(ed25519.PublicKey))

		go func() {
			log.Printf("NVP Control Plane PRODUCTION listening on %s db=%s", *addr, *dbPath)
			if err := srv.ListenAndServe(*addr); err != nil {
				log.Printf("server stopped: %v", err)
			}
		}()
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	} else {
		stub := server.NewStub(cfg)
		stub.RegisterLicense(model.LicenseRecord{
			LicenseID:  "nyx_lic_demo",
			Plan:       "premium",
			MaxDevices: 3,
			Enabled:    true,
			ExpiresAt:  time.Now().Add(365 * 24 * time.Hour),
			Locations:  []string{"fi-hel"},
		})
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
