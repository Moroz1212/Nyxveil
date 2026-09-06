package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/model"
)

// Config is defined in config.go

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
	s.mux.HandleFunc("GET /api/v1/locations", s.handleLocations)
	s.mux.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	s.mux.HandleFunc("GET /api/v1/revocation", s.handleRevocation)
	s.mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	s.mux.HandleFunc("POST /api/v1/master/access", s.handleMasterAccess)
}

// ListenAndServe starts the HTTP server.
func (s *Stub) ListenAndServe(addr string) error {
	srv, err := startHTTPServer(addr, s.mux, s.cfg.Options)
	s.srv = srv
	return err
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
	if !ok || !licenseTokenValid(lic, req.LicenseToken) || !licenseUsable(lic) {
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
	if !ok || !licenseTokenValid(lic, req.LicenseToken) || !licenseUsable(lic) {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	if !validDevicePublicKey(req.PublicKey) || req.DeviceID == "" {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	if existing, ok := s.devices[req.DeviceID]; ok {
		if existing.LicenseID != licID || existing.Revoked {
			writeJSON(w, api.DeviceActivateResponse{Activated: false})
			return
		}
		existing.PublicKey = req.PublicKey
		existing.Enabled = true
		writeJSON(w, api.DeviceActivateResponse{DeviceID: req.DeviceID, Activated: true})
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
	d, ok := s.devices[req.DeviceID]
	lic, licOK := s.licenses[licenseIDFromToken(req.LicenseToken)]
	if !ok || !licOK || d.LicenseID != lic.LicenseID || !licenseTokenValid(lic, req.LicenseToken) {
		s.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d.Revoked = true
	s.revoked.RevokeDevice(req.DeviceID)
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
	if !ok || !licenseTokenValid(lic, req.LicenseToken) || !licenseUsable(lic) || !devOK || dev.Revoked || !dev.Enabled || !validDevicePublicKey(dev.PublicKey) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	role := ticket.RoleForPlan(lic.Plan)
	perms := ticket.PermissionsForPlan(lic.Plan)
	var nodeScope []string
	if req.NodeID != "" {
		nodeScope = []string{req.NodeID}
	}
	// Location-scoped: LocationID set + NodeID empty → Locations=[location], NodeScope empty.
	locations := lic.Locations
	if req.LocationID != "" {
		locations = []string{req.LocationID}
		if len(lic.Locations) > 0 && !containsString(lic.Locations, req.LocationID) {
			http.Error(w, "forbidden location", http.StatusForbidden)
			return
		}
	}
	tok, err := ticket.IssueScoped(s.cfg.Issuer, licID, req.DeviceID, role, lic.Plan, perms, locations, nodeScope, dev.PublicKey)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exp := time.Now().Add(s.cfg.Issuer.TTL).Unix()
	writeJSON(w, api.TicketIssueResponse{AccessTicket: tok, ExpiresAt: exp, NodeID: req.NodeID})
}

func (s *Stub) handleTicketRefresh(w http.ResponseWriter, r *http.Request) {
	var req api.TicketRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.AccessTicket == "" || req.DeviceID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	verifier := ticket.VerifierConfig{
		Issuer:     s.cfg.Issuer.Issuer,
		Audience:   s.cfg.Issuer.Audience,
		PublicKeys: PublicKeys(s.cfg),
		Revoked:    s.revoked,
	}
	old, err := ticket.VerifyIdentity(verifier, req.AccessTicket, req.DeviceID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	lic, ok := s.licenses[old.LicenseID]
	dev, devOK := s.devices[req.DeviceID]
	s.mu.RUnlock()
	if !ok || !licenseUsable(lic) || !devOK || dev.Revoked || !dev.Enabled || dev.LicenseID != old.LicenseID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if req.LicenseToken != "" {
		if licenseIDFromToken(req.LicenseToken) != old.LicenseID || !licenseTokenValid(lic, req.LicenseToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	tok, err := buildRefreshTicket(s.cfg.Issuer, old, lic, dev.PublicKey)
	if err != nil {
		if errors.Is(err, errRefreshNoLocations) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, api.TicketIssueResponse{
		AccessTicket: tok,
		ExpiresAt:    time.Now().Add(s.cfg.Issuer.TTL).Unix(),
	})
}

func (s *Stub) handleCatalog(w http.ResponseWriter, r *http.Request) {
	role := catalogRoleFromRequest(r, s.cfg)
	cat := s.cfg.Catalog
	cat.Nodes = copyFilterNodes(cat.Nodes, role)
	signed, err := s.cfg.CatalogSigner.Sign(cat)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, signed)
}

func (s *Stub) handleRevocation(w http.ResponseWriter, r *http.Request) {
	jtis, licenses, devices := s.revoked.Snapshot()
	writeJSON(w, api.RevocationListResponse{
		RevokedJTIs:     jtis,
		RevokedLicenses: licenses,
		RevokedDevices:  devices,
		UpdatedAt:       time.Now().Unix(),
	})
}

func (s *Stub) handleLocations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.cfg.Catalog.Locations)
}

func (s *Stub) handleNodes(w http.ResponseWriter, r *http.Request) {
	role := catalogRoleFromRequest(r, s.cfg)
	writeJSON(w, copyFilterNodes(s.cfg.Catalog.Nodes, role))
}

func (s *Stub) handleMasterAccess(w http.ResponseWriter, r *http.Request) {
	var req MasterAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	lic, ok := s.licenses[licenseIDFromToken(req.LicenseToken)]
	s.mu.RUnlock()
	granted := ok && licenseTokenValid(lic, req.LicenseToken) && licenseUsable(lic) && lic.Plan == "master"
	writeJSON(w, MasterAccessResponse{Role: "master", Granted: granted})
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
