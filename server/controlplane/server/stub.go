package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nyxveil/nvp/auth/ticket"
	"github.com/nyxveil/nvp/controlplane/api"
	"github.com/nyxveil/nvp/controlplane/catalog"
	"github.com/nyxveil/nvp/controlplane/model"
)

// Config holds Control Plane stub configuration.
type Config struct {
	Issuer        ticket.IssuerConfig
	CatalogSigner catalog.Signer
	Catalog       model.Catalog
	MaxDevices    map[string]int // license_id -> max
}

// Stub is a minimal Control Plane HTTP server for development and tests.
type Stub struct {
	cfg      Config
	mux      *http.ServeMux
	srv      *http.Server
	mu       sync.RWMutex
	licenses map[string]*model.LicenseRecord
	devices  map[string]*model.DeviceRecord // device_id -> record
	revoked  *ticket.MemoryRevocation
}

// NewStub creates a new Control Plane stub.
func NewStub(cfg Config) *Stub {
	if cfg.MaxDevices == nil {
		cfg.MaxDevices = make(map[string]int)
	}
	s := &Stub{
		cfg:      cfg,
		mux:      http.NewServeMux(),
		licenses: make(map[string]*model.LicenseRecord),
		devices:  make(map[string]*model.DeviceRecord),
		revoked:  ticket.NewMemoryRevocation(),
	}
	s.routes()
	return s
}

// RegisterLicense adds a license for testing.
func (s *Stub) RegisterLicense(rec model.LicenseRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.licenses[rec.LicenseID] = &rec
}

// HTTPHandler returns the HTTP handler for testing and embedding.
func (s *Stub) HTTPHandler() http.Handler {
	return s.mux
}

func (s *Stub) routes() {
	s.mux.HandleFunc("POST /api/v1/license/validate", s.handleLicenseValidate)
	s.mux.HandleFunc("POST /api/v1/device/activate", s.handleDeviceActivate)
	s.mux.HandleFunc("POST /api/v1/device/remove", s.handleDeviceRemove)
	s.mux.HandleFunc("POST /api/v1/ticket/issue", s.handleTicketIssue)
	s.mux.HandleFunc("POST /api/v1/ticket/refresh", s.handleTicketRefresh)
	s.mux.HandleFunc("GET /api/v1/catalog", s.handleCatalog)
	s.mux.HandleFunc("GET /api/v1/revocation", s.handleRevocation)
	s.mux.HandleFunc("GET /api/v1/version", s.handleVersion)
}

// ListenAndServe starts the HTTP server.
func (s *Stub) ListenAndServe(addr string) error {
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Stub) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

func (s *Stub) handleLicenseValidate(w http.ResponseWriter, r *http.Request) {
	var req api.LicenseValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	licID := licenseIDFromToken(req.LicenseToken)
	s.mu.RLock()
	lic, ok := s.licenses[licID]
	s.mu.RUnlock()

	resp := api.LicenseValidateResponse{}
	if !ok || !lic.Enabled || lic.Revoked {
		resp.Valid = false
		resp.Message = "invalid or revoked license"
	} else if time.Now().After(lic.ExpiresAt) {
		resp.Valid = false
		resp.Message = "license expired"
	} else {
		resp.Valid = true
		resp.LicenseID = lic.LicenseID
		resp.Plan = lic.Plan
		resp.MaxDevices = lic.MaxDevices
	}
	writeJSON(w, resp)
}

func (s *Stub) handleDeviceActivate(w http.ResponseWriter, r *http.Request) {
	var req api.DeviceActivateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	licID := licenseIDFromToken(req.LicenseToken)
	s.mu.Lock()
	defer s.mu.Unlock()
	lic, ok := s.licenses[licID]
	if !ok || !lic.Enabled || lic.Revoked {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	count := 0
	for _, d := range s.devices {
		if d.LicenseID == licID && !d.Revoked {
			count++
		}
	}
	maxDev := lic.MaxDevices
	if maxDev <= 0 {
		maxDev = 3
	}
	if count >= maxDev {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	s.devices[req.DeviceID] = &model.DeviceRecord{
		DeviceID:   req.DeviceID,
		LicenseID:  licID,
		PublicKey:  req.PublicKey,
		Enabled:    true,
		Registered: time.Now().UTC(),
	}
	writeJSON(w, api.DeviceActivateResponse{DeviceID: req.DeviceID, Activated: true})
}

func (s *Stub) handleDeviceRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LicenseToken string `json:"license_token"`
		DeviceID     string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if d, ok := s.devices[req.DeviceID]; ok {
		d.Revoked = true
		s.revoked.RevokeDevice(req.DeviceID)
	}
	s.mu.Unlock()
	writeJSON(w, map[string]bool{"removed": true})
}

func (s *Stub) handleTicketIssue(w http.ResponseWriter, r *http.Request) {
	var req api.TicketIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	licID := licenseIDFromToken(req.LicenseToken)
	s.mu.RLock()
	lic, ok := s.licenses[licID]
	dev, devOK := s.devices[req.DeviceID]
	s.mu.RUnlock()
	if !ok || !lic.Enabled || lic.Revoked || !devOK || dev.Revoked || !dev.Enabled {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	role := "user"
	if lic.Plan == "master" {
		role = "master"
	}
	tok, err := ticket.Issue(s.cfg.Issuer, licID, req.DeviceID, role, lic.Plan, []string{"connect"}, lic.Locations)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exp := time.Now().Add(s.cfg.Issuer.TTL).Unix()
	writeJSON(w, api.TicketIssueResponse{AccessTicket: tok, ExpiresAt: exp, NodeID: req.NodeID})
}

func (s *Stub) handleTicketRefresh(w http.ResponseWriter, r *http.Request) {
	s.handleTicketIssue(w, r)
}

func (s *Stub) handleCatalog(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	cat := s.cfg.Catalog
	if role != "master" {
		filtered := make([]model.NodeRegistryEntry, 0)
		for _, n := range cat.Nodes {
			if !n.TestOnly {
				filtered = append(filtered, n)
			}
		}
		cat.Nodes = filtered
	}
	signed, err := s.cfg.CatalogSigner.Sign(cat)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, signed)
}

func (s *Stub) handleRevocation(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.RevocationListResponse{UpdatedAt: time.Now().Unix()})
}

func (s *Stub) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.VersionResponse{
		ControlPlaneVersion: "0.1.0-stub",
		MinProtocolVersion:  1,
		MaxProtocolVersion:  1,
		RecommendedClient:   "0.1.0",
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func licenseIDFromToken(token string) string {
	// Stub: token format nyx_lic_<id> maps to license_id
	if strings.HasPrefix(token, "nyx_lic_") {
		return token
	}
	return token
}

// RevokeLicense revokes a license (testing helper).
func (s *Stub) RevokeLicense(licenseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lic, ok := s.licenses[licenseID]; ok {
		lic.Revoked = true
	}
	s.revoked.RevokeLicense(licenseID)
}

// PublicKeys returns CP verification keys for nodes.
func (s *Stub) PublicKeys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{s.cfg.Issuer.KeyID: s.cfg.Issuer.PrivateKey.Public().(ed25519.PublicKey)}
}
