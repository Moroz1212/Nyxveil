package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/controlplane/api"
	"github.com/nyxveil/nvp/controlplane/model"
	"github.com/nyxveil/nvp/controlplane/store"
)

// ProductionServer is Control Plane with SQLite persistence.
type ProductionServer struct {
	cfg     Config
	store   *store.Store
	mux     *http.ServeMux
	srv     *http.Server
	mu      sync.RWMutex
	revoked *ticket.MemoryRevocation
}

// NewProduction creates persistent Control Plane server.
func NewProduction(cfg Config, st *store.Store) *ProductionServer {
	p := &ProductionServer{
		cfg:     cfg,
		store:   st,
		mux:     http.NewServeMux(),
		revoked: ticket.NewMemoryRevocation(),
	}
	p.routes()
	return p
}

func (p *ProductionServer) routes() {
	p.mux.HandleFunc("POST /api/v1/license/validate", p.handleLicenseValidate)
	p.mux.HandleFunc("POST /api/v1/device/activate", p.handleDeviceActivate)
	p.mux.HandleFunc("POST /api/v1/device/remove", p.handleDeviceRemove)
	p.mux.HandleFunc("POST /api/v1/ticket/issue", p.handleTicketIssue)
	p.mux.HandleFunc("POST /api/v1/ticket/refresh", p.handleTicketIssue)
	p.mux.HandleFunc("GET /api/v1/catalog", p.handleCatalog)
	p.mux.HandleFunc("GET /api/v1/revocation", p.handleRevocation)
	p.mux.HandleFunc("GET /api/v1/version", p.handleVersion)
	p.mux.HandleFunc("POST /api/v1/nodes/{node_id}/health", p.handleNodeHealth)
}

func (p *ProductionServer) HTTPHandler() http.Handler { return p.mux }

func (p *ProductionServer) ListenAndServe(addr string) error {
	p.srv = &http.Server{Addr: addr, Handler: p.mux, ReadHeaderTimeout: 10 * time.Second}
	return p.srv.ListenAndServe()
}

func (p *ProductionServer) Shutdown(ctx context.Context) error {
	if p.srv == nil {
		return nil
	}
	return p.srv.Shutdown(ctx)
}

func (p *ProductionServer) handleLicenseValidate(w http.ResponseWriter, r *http.Request) {
	var req api.LicenseValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lic, err := p.store.GetLicense(r.Context(), licenseIDFromToken(req.LicenseToken))
	resp := api.LicenseValidateResponse{}
	if err != nil || !lic.Enabled || lic.Revoked || time.Now().After(lic.ExpiresAt) {
		resp.Valid = false
		resp.Message = "invalid or expired license"
	} else {
		resp.Valid = true
		resp.LicenseID = lic.LicenseID
		resp.Plan = lic.Plan
		resp.MaxDevices = lic.MaxDevices
	}
	writeJSON(w, resp)
}

func (p *ProductionServer) handleDeviceActivate(w http.ResponseWriter, r *http.Request) {
	var req api.DeviceActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	licID := licenseIDFromToken(req.LicenseToken)
	lic, err := p.store.GetLicense(r.Context(), licID)
	if err != nil || !lic.Enabled || lic.Revoked {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	count, _ := p.store.CountDevices(r.Context(), licID)
	max := lic.MaxDevices
	if max <= 0 {
		max = 3
	}
	if count >= max {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	_ = p.store.RegisterDevice(r.Context(), model.DeviceRecord{
		DeviceID:   req.DeviceID,
		LicenseID:  licID,
		PublicKey:  req.PublicKey,
		Enabled:    true,
		Registered: time.Now().UTC(),
	})
	writeJSON(w, api.DeviceActivateResponse{DeviceID: req.DeviceID, Activated: true})
}

func (p *ProductionServer) handleDeviceRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string `json:"device_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	_ = p.store.Revoke(r.Context(), "device", req.DeviceID)
	p.revoked.RevokeDevice(req.DeviceID)
	writeJSON(w, map[string]bool{"removed": true})
}

func (p *ProductionServer) handleTicketIssue(w http.ResponseWriter, r *http.Request) {
	var req api.TicketIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	licID := licenseIDFromToken(req.LicenseToken)
	lic, err := p.store.GetLicense(r.Context(), licID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dev, err := p.store.GetDevice(r.Context(), req.DeviceID)
	if err != nil || dev.Revoked || !dev.Enabled || dev.LicenseID != licID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if lic.Revoked || !lic.Enabled || time.Now().After(lic.ExpiresAt) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	role := "user"
	if lic.Plan == "master" {
		role = "master"
	}
	tok, err := ticket.Issue(p.cfg.Issuer, licID, req.DeviceID, role, lic.Plan, []string{"connect"}, lic.Locations)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, api.TicketIssueResponse{
		AccessTicket: tok,
		ExpiresAt:    time.Now().Add(p.cfg.Issuer.TTL).Unix(),
		NodeID:       req.NodeID,
	})
}

func (p *ProductionServer) handleCatalog(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	nodes, err := p.store.ListNodes(r.Context())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if role != "master" {
		filtered := nodes[:0]
		for _, n := range nodes {
			if !n.TestOnly {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	cat := p.cfg.Catalog
	cat.Nodes = nodes
	signed, err := p.cfg.CatalogSigner.Sign(cat)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, signed)
}

func (p *ProductionServer) handleRevocation(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.RevocationListResponse{UpdatedAt: time.Now().Unix()})
}

func (p *ProductionServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.VersionResponse{
		ControlPlaneVersion: "1.0.0",
		MinProtocolVersion:  1,
		MaxProtocolVersion:  1,
		RecommendedClient:   "1.0.0",
	})
}

func (p *ProductionServer) handleNodeHealth(w http.ResponseWriter, r *http.Request) {
	var req api.NodeHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := p.store.ValidateNodeToken(r.Context(), req.NodeID); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = p.store.UpsertNode(r.Context(), model.NodeRegistryEntry{
		NodeID:          req.NodeID,
		CurrentSessions: req.CurrentSessions,
		Capacity:        req.Capacity,
		LastSeen:        time.Now().UTC(),
		Health:          model.HealthInfo{Healthy: true, SessionCount: req.CurrentSessions},
	})
	writeJSON(w, map[string]string{"status": "ok"})
}

// RegisterLicense persists license.
func (p *ProductionServer) RegisterLicense(ctx context.Context, lic model.LicenseRecord) error {
	return p.store.UpsertLicense(ctx, lic)
}

// RegisterNodeIdentity registers node for heartbeat auth.
func (p *ProductionServer) RegisterNodeIdentity(ctx context.Context, nodeID string, pub ed25519.PublicKey) error {
	return p.store.RegisterNodeIdentity(ctx, nodeID, pub)
}
