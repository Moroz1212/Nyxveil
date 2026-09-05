package server

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/controlplane/store"
)

// ProductionServer is Control Plane with SQLite persistence.
type ProductionServer struct {
	cfg     Config
	store   *store.Store
	mux     *http.ServeMux
	srv     *http.Server
	revoked *ticket.MemoryRevocation
}

// NewProduction creates persistent Control Plane server.
func NewProduction(cfg Config, st *store.Store) *ProductionServer {
	if cfg.Options.RateLimit.RequestsPerMinute == 0 {
		cfg.Options.RateLimit = DefaultRateLimit()
	}
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
	p.mux.HandleFunc("POST /api/v1/ticket/refresh", p.handleTicketRefresh)
	p.mux.HandleFunc("GET /api/v1/catalog", p.handleCatalog)
	p.mux.HandleFunc("GET /api/v1/locations", p.handleLocations)
	p.mux.HandleFunc("GET /api/v1/nodes", p.handleNodes)
	p.mux.HandleFunc("GET /api/v1/revocation", p.handleRevocation)
	p.mux.HandleFunc("GET /api/v1/version", p.handleVersion)
	p.mux.HandleFunc("POST /api/v1/master/access", p.handleMasterAccess)
	p.mux.HandleFunc("POST /api/v1/nodes/{node_id}/health", p.handleNodeHealth)
	p.mux.HandleFunc("POST /api/v1/nodes/{node_id}/drain", p.handleNodeDrain)
	p.mux.HandleFunc("POST /api/v1/nodes/{node_id}/maintenance", p.handleNodeMaintenance)
}

func (p *ProductionServer) HTTPHandler() http.Handler { return p.mux }

func (p *ProductionServer) ListenAndServe(addr string) error {
	srv, err := startHTTPServer(addr, p.mux, p.cfg.Options)
	p.srv = srv
	return err
}

func (p *ProductionServer) Shutdown(ctx context.Context) error {
	if p.srv == nil {
		return nil
	}
	return p.srv.Shutdown(ctx)
}

func (p *ProductionServer) ticketVerifier() ticket.VerifierConfig {
	return ticket.VerifierConfig{
		Issuer:     p.cfg.Issuer.Issuer,
		Audience:   p.cfg.Issuer.Audience,
		PublicKeys: PublicKeys(p.cfg),
		Revoked:    p.revoked,
	}
}

func (p *ProductionServer) authenticateCatalog(r *http.Request) (*catalogCaller, error) {
	tok := bearerToken(r)
	if tok == "" {
		return nil, errUnauthorized
	}
	if looksLikeLicenseToken(tok) {
		lic, err := p.store.GetLicense(r.Context(), licenseIDFromToken(tok))
		if err != nil || !licenseTokenValidWith(p.store, lic, tok) || !licenseUsable(lic) {
			return nil, errUnauthorized
		}
		role := ticket.RoleForPlan(lic.Plan)
		return &catalogCaller{Role: role, Locations: append([]string(nil), lic.Locations...)}, nil
	}
	claims, err := ticket.VerifyIdentity(p.ticketVerifier(), tok, "")
	if err != nil {
		return nil, errUnauthorized
	}
	lic, err := p.store.GetLicense(r.Context(), claims.LicenseID)
	if err != nil || !licenseUsable(lic) {
		return nil, errUnauthorized
	}
	role := claims.Role
	if role == "" {
		role = "user"
	}
	locs := claims.Locations
	if len(locs) == 0 {
		locs = lic.Locations
	}
	return &catalogCaller{Role: role, Locations: append([]string(nil), locs...)}, nil
}

func (p *ProductionServer) requireNodeAuth(r *http.Request) error {
	nodeID := r.Header.Get("X-Node-ID")
	if nodeID == "" {
		nodeID = r.URL.Query().Get("node_id")
	}
	token := bearerToken(r)
	if token == "" {
		token = r.URL.Query().Get("node_token")
	}
	if nodeID == "" || token == "" {
		return errUnauthorized
	}
	// Reject license-shaped user credentials even if somehow registered.
	if looksLikeLicenseToken(token) {
		return errForbidden
	}
	if err := p.store.ValidateNodeToken(r.Context(), nodeID, token); err != nil {
		return errUnauthorized
	}
	return nil
}

var (
	errUnauthorized = &httpError{code: http.StatusUnauthorized, msg: "unauthorized"}
	errForbidden    = &httpError{code: http.StatusForbidden, msg: "forbidden"}
)

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func writeHTTPError(w http.ResponseWriter, err error) {
	if he, ok := err.(*httpError); ok {
		http.Error(w, he.msg, he.code)
		return
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

func (p *ProductionServer) handleLicenseValidate(w http.ResponseWriter, r *http.Request) {
	var req api.LicenseValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lic, err := p.store.GetLicense(r.Context(), licenseIDFromToken(req.LicenseToken))
	resp := api.LicenseValidateResponse{}
	if err != nil || !licenseTokenValidWith(p.store, lic, req.LicenseToken) || !licenseUsable(lic) {
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
	if err != nil || !licenseTokenValidWith(p.store, lic, req.LicenseToken) || !licenseUsable(lic) {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	if !validDevicePublicKey(req.PublicKey) || req.DeviceID == "" {
		writeJSON(w, api.DeviceActivateResponse{Activated: false})
		return
	}
	if existing, getErr := p.store.GetDevice(r.Context(), req.DeviceID); getErr == nil {
		if existing.LicenseID != licID || existing.Revoked {
			writeJSON(w, api.DeviceActivateResponse{Activated: false})
			return
		}
		_ = p.store.RegisterDevice(r.Context(), model.DeviceRecord{
			DeviceID:   req.DeviceID,
			LicenseID:  licID,
			PublicKey:  req.PublicKey,
			Enabled:    true,
			Registered: existing.Registered,
		})
		writeJSON(w, api.DeviceActivateResponse{DeviceID: req.DeviceID, Activated: true})
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
		LicenseToken string `json:"license_token"`
		DeviceID     string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lic, err := p.store.GetLicense(r.Context(), licenseIDFromToken(req.LicenseToken))
	if err != nil || !licenseTokenValidWith(p.store, lic, req.LicenseToken) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dev, err := p.store.GetDevice(r.Context(), req.DeviceID)
	if err != nil || dev.LicenseID != lic.LicenseID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	_ = p.store.RevokeDevice(r.Context(), req.DeviceID)
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
	if err != nil || !licenseTokenValidWith(p.store, lic, req.LicenseToken) || !licenseUsable(lic) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dev, err := p.store.GetDevice(r.Context(), req.DeviceID)
	if err != nil || dev.Revoked || !dev.Enabled || dev.LicenseID != licID || !validDevicePublicKey(dev.PublicKey) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	role := ticket.RoleForPlan(lic.Plan)
	perms := ticket.PermissionsForPlan(lic.Plan)
	var nodeScope []string
	if req.NodeID != "" {
		nodeScope = []string{req.NodeID}
	}
	// Location-scoped ticket model (final):
	//   - NodeID empty + LocationID set → Locations=[location] (must be allowed), NodeScope empty.
	//   - Empty NodeScope means any node in Locations (same-location failover with one ticket).
	//   - Cross-location requires Locations to contain each location tried.
	locations := lic.Locations
	if req.LocationID != "" {
		locations = []string{req.LocationID}
		if len(lic.Locations) > 0 && !containsString(lic.Locations, req.LocationID) {
			http.Error(w, "forbidden location", http.StatusForbidden)
			return
		}
	}
	tok, err := ticket.IssueScoped(p.cfg.Issuer, licID, req.DeviceID, role, lic.Plan, perms, locations, nodeScope, dev.PublicKey)
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

func (p *ProductionServer) handleTicketRefresh(w http.ResponseWriter, r *http.Request) {
	var req api.TicketRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.AccessTicket == "" || req.DeviceID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Validate old ticket (crypto, device binding, revocation).
	old, err := ticket.VerifyIdentity(p.ticketVerifier(), req.AccessTicket, req.DeviceID)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if old.DeviceID != req.DeviceID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	licID := old.LicenseID
	if req.LicenseToken != "" {
		tokenLic := licenseIDFromToken(req.LicenseToken)
		if tokenLic != licID {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		lic, licErr := p.store.GetLicense(r.Context(), tokenLic)
		if licErr != nil || !licenseTokenValidWith(p.store, lic, req.LicenseToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	// Load CURRENT license + device; reject disabled/expired/revoked.
	lic, err := p.store.GetLicense(r.Context(), licID)
	if err != nil || !licenseUsable(lic) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dev, err := p.store.GetDevice(r.Context(), req.DeviceID)
	if err != nil || dev.Revoked || !dev.Enabled || dev.LicenseID != licID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Rebuild from CURRENT entitlements — do not ticket.Reissue (copies stale rights).
	tok, err := buildRefreshTicket(p.cfg.Issuer, old, lic, dev.PublicKey)
	if err != nil {
		if errors.Is(err, errRefreshNoLocations) || errors.Is(err, errRefreshNoNodeScope) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, api.TicketIssueResponse{
		AccessTicket: tok,
		ExpiresAt:    time.Now().Add(p.cfg.Issuer.TTL).Unix(),
	})
}

func (p *ProductionServer) handleCatalog(w http.ResponseWriter, r *http.Request) {
	caller, err := p.authenticateCatalog(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	nodes, err := p.store.ListNodes(r.Context())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if len(nodes) == 0 {
		nodes = append([]model.NodeRegistryEntry(nil), p.cfg.Catalog.Nodes...)
	}
	nodes = filterNodesByLocations(nodes, caller.Locations)
	nodes = copyFilterNodes(nodes, caller.Role)
	cat := p.cfg.Catalog
	cat.Locations = filterLocationsByIDs(cat.Locations, caller.Locations)
	cat.Nodes = nodes
	signed, err := p.cfg.CatalogSigner.Sign(cat)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, signed)
}

func (p *ProductionServer) handleLocations(w http.ResponseWriter, r *http.Request) {
	caller, err := p.authenticateCatalog(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	writeJSON(w, filterLocationsByIDs(p.cfg.Catalog.Locations, caller.Locations))
}

func (p *ProductionServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	caller, err := p.authenticateCatalog(r)
	if err != nil {
		writeHTTPError(w, err)
		return
	}
	nodes, err := p.store.ListNodes(r.Context())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if len(nodes) == 0 {
		nodes = append([]model.NodeRegistryEntry(nil), p.cfg.Catalog.Nodes...)
	}
	nodes = filterNodesByLocations(nodes, caller.Locations)
	nodes = copyFilterNodes(nodes, caller.Role)
	writeJSON(w, nodes)
}

func (p *ProductionServer) handleRevocation(w http.ResponseWriter, r *http.Request) {
	if err := p.requireNodeAuth(r); err != nil {
		writeHTTPError(w, err)
		return
	}
	list, err := p.store.ListRevocations(r.Context())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, mergeRevocation(p.revoked, list))
}

func (p *ProductionServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, api.VersionResponse{
		ControlPlaneVersion: "1.0.0",
		MinProtocolVersion:  1,
		MaxProtocolVersion:  1,
		RecommendedClient:   "1.0.0",
	})
}

func (p *ProductionServer) handleMasterAccess(w http.ResponseWriter, r *http.Request) {
	var req MasterAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lic, err := p.store.GetLicense(r.Context(), licenseIDFromToken(req.LicenseToken))
	granted := err == nil && licenseTokenValidWith(p.store, lic, req.LicenseToken) && licenseUsable(lic) && lic.Plan == "master"
	writeJSON(w, MasterAccessResponse{Role: "master", Granted: granted})
}

func (p *ProductionServer) handleNodeHealth(w http.ResponseWriter, r *http.Request) {
	var req api.NodeHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	nodeID := r.PathValue("node_id")
	if nodeID == "" {
		nodeID = req.NodeID
	}
	if err := p.store.ValidateNodeToken(r.Context(), nodeID, req.NodeToken); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	health := model.NodeHealth{Healthy: true, SessionCount: req.CurrentSessions}
	if err := p.store.UpdateNodeHealth(r.Context(), nodeID, health, req.CurrentSessions, req.Capacity, time.Now().UTC()); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (p *ProductionServer) handleNodeDrain(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	var req NodeDrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := p.store.ValidateNodeToken(r.Context(), nodeID, req.NodeToken); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := p.store.SetNodeDrain(r.Context(), nodeID, req.Draining); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"draining": req.Draining})
}

func (p *ProductionServer) handleNodeMaintenance(w http.ResponseWriter, r *http.Request) {
	nodeID := r.PathValue("node_id")
	var req NodeMaintenanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := p.store.ValidateNodeToken(r.Context(), nodeID, req.NodeToken); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := p.store.SetNodeMaintenance(r.Context(), nodeID, req.Enabled); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"enabled": req.Enabled})
}

// RegisterLicense persists license.
func (p *ProductionServer) RegisterLicense(ctx context.Context, lic model.LicenseRecord) error {
	return p.store.UpsertLicense(ctx, lic)
}

// RegisterNodeIdentity registers node for heartbeat auth.
func (p *ProductionServer) RegisterNodeIdentity(ctx context.Context, nodeID string, pub ed25519.PublicKey) error {
	return p.store.RegisterNodeIdentity(ctx, nodeID, pub)
}

// Store returns the underlying persistence store (tests/admin).
func (p *ProductionServer) Store() *store.Store { return p.store }

// RevocationCache returns in-memory revocation overlay for nodes.
func (p *ProductionServer) RevocationCache() *ticket.MemoryRevocation {
	return p.revoked
}
