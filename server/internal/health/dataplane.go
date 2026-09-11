package health

import (
	"encoding/json"
	"fmt"
)

// DataplaneOK reports VPN dataplane readiness independent of Control Plane connectivity.
// Required for update commit when management plane was already degraded pre-update.
//
// DATAPLANE_REQUIRED: running, accepting, bridge_ok, tls_ok, quic_ok, tun_ready,
// identity_present, and not version_blocked (skip_tun exempts tun/bridge).
func (s *Status) DataplaneOK() bool {
	if s == nil {
		return false
	}
	if !s.Running || !s.Accepting {
		return false
	}
	if !s.IdentityPresent || s.VersionBlocked {
		return false
	}
	if !s.TLSOK || !s.QUICOK {
		return false
	}
	if !s.SkipTUN {
		if !s.TUNReady || !s.BridgeOK {
			return false
		}
	}
	return true
}

// ManagementConnected is Control Plane connectivity (independent of dataplane).
func (s *Status) ManagementConnected() bool {
	return s != nil && s.CPConnected
}

// Baseline is a pre-update / pre-rollback snapshot used for regression gates.
type Baseline struct {
	LifecycleKnown  bool   `json:"lifecycle_known"`
	Draining        bool   `json:"draining"`
	MaintenanceMode bool   `json:"maintenance_mode"`
	NodeID          string `json:"node_id,omitempty"`
	ConfigVersion   int64  `json:"config_version,omitempty"`
	Running         bool   `json:"running"`
	Accepting       bool   `json:"accepting"`
	BridgeOK        bool   `json:"bridge_ok"`
	TLSOK           bool   `json:"tls_ok"`
	QUICOK          bool   `json:"quic_ok"`
	TUNReady        bool   `json:"tun_ready"`
	CPConnected     bool   `json:"cp_connected"`
	Healthy         bool   `json:"healthy"`
	IdentityPresent bool   `json:"identity_present"`
	VersionBlocked  bool   `json:"version_blocked"`
	DataplaneOK     bool   `json:"dataplane_ok"`
}

// CaptureBaseline extracts update-relevant fields from a full status snapshot.
func CaptureBaseline(s Status) Baseline {
	s.Healthy = s.ComputeHealthy()
	return Baseline{
		LifecycleKnown: true, Draining: s.Draining, MaintenanceMode: s.MaintenanceMode,
		NodeID: s.NodeID, ConfigVersion: s.ConfigVersion,
		Running:         s.Running,
		Accepting:       s.Accepting,
		BridgeOK:        s.BridgeOK,
		TLSOK:           s.TLSOK,
		QUICOK:          s.QUICOK,
		TUNReady:        s.TUNReady || s.SkipTUN,
		CPConnected:     s.CPConnected,
		Healthy:         s.Healthy,
		IdentityPresent: s.IdentityPresent,
		VersionBlocked:  s.VersionBlocked,
		DataplaneOK:     s.DataplaneOK(),
	}
}

// ParseStatusJSON unmarshals a /status (or enriched /health) payload.
func ParseStatusJSON(b []byte) (Status, error) {
	var s Status
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("health: parse status JSON: %w", err)
	}
	return s, nil
}

// DataplaneRegressed reports whether post lost a dataplane capability that pre had.
func DataplaneRegressed(pre, post Baseline) (bool, string) {
	checks := []struct {
		name string
		pre  bool
		post bool
	}{
		{"running", pre.Running, post.Running},
		{"accepting", pre.Accepting, post.Accepting},
		{"bridge_ok", pre.BridgeOK, post.BridgeOK},
		{"tls_ok", pre.TLSOK, post.TLSOK},
		{"quic_ok", pre.QUICOK, post.QUICOK},
		{"tun_ready", pre.TUNReady, post.TUNReady},
		{"identity_present", pre.IdentityPresent, post.IdentityPresent},
	}
	for _, c := range checks {
		if c.pre && !c.post {
			return true, c.name
		}
	}
	if !pre.VersionBlocked && post.VersionBlocked {
		return true, "version_blocked"
	}
	if pre.DataplaneOK && !post.DataplaneOK {
		return true, "dataplane_ok"
	}
	return false, ""
}

// UpdateResult summarizes post-update health evaluation (no secrets).
type UpdateResult struct {
	OK                               bool   `json:"ok"`
	UpdateSuccess                    bool   `json:"update_success"`
	DataplaneHealthy                 bool   `json:"dataplane_healthy"`
	ManagementPlaneConnected         bool   `json:"management_plane_connected"`
	PreexistingManagementDegradation bool   `json:"preexisting_management_degradation"`
	Reason                           string `json:"reason,omitempty"`
}

// EvaluatePostUpdate decides whether an update may commit given pre-update baseline and post status.
func EvaluatePostUpdate(pre Baseline, post Status) UpdateResult {
	if pre.IntentionallyStopped() {
		reason := stoppedUpdateFailure(pre, post)
		return UpdateResult{OK: reason == "", UpdateSuccess: reason == "", Reason: reason,
			DataplaneHealthy: post.DataplaneOK(), ManagementPlaneConnected: post.CPConnected,
			PreexistingManagementDegradation: !pre.CPConnected}
	}
	postBase := CaptureBaseline(post)
	res := UpdateResult{
		DataplaneHealthy:                 postBase.DataplaneOK,
		ManagementPlaneConnected:         postBase.CPConnected,
		PreexistingManagementDegradation: !pre.CPConnected,
	}
	if pre.NodeID != "" && pre.NodeID != post.NodeID || post.ConfigVersion < pre.ConfigVersion {
		res.Reason = "identity/configuration changed"
		return res
	}

	if regressed, field := DataplaneRegressed(pre, postBase); regressed {
		res.Reason = "dataplane regression: " + field
		return res
	}
	if !postBase.DataplaneOK {
		res.Reason = "dataplane not healthy after update"
		return res
	}

	// Strict: if CP was connected before, must regain connectivity.
	if pre.CPConnected && !postBase.CPConnected {
		res.Reason = "cp_connected regressed from true to false"
		return res
	}
	if pre.Healthy && !postBase.Healthy && pre.CPConnected {
		// Full health was true; require it back when management was healthy.
		res.Reason = "global healthy regressed"
		return res
	}

	res.OK = true
	res.UpdateSuccess = true
	if !postBase.CPConnected {
		res.Reason = "update committed with preexisting management-plane degradation"
	}
	return res
}

// RollbackResult summarizes post-rollback evaluation against pre-update baseline.
type RollbackResult struct {
	Complete         bool   `json:"rollback_complete"`
	BaselineRestored bool   `json:"baseline_restored"`
	Incomplete       bool   `json:"rollback_incomplete"`
	Reason           string `json:"reason,omitempty"`
	TLSOwnershipFail bool   `json:"tls_ownership_mismatch,omitempty"`
}

// EvaluateRollbackSuccess reports success when restored state is not worse than pre-update baseline.
func EvaluateRollbackSuccess(pre Baseline, post Status) RollbackResult {
	if pre.IntentionallyStopped() {
		reason := stoppedUpdateFailure(pre, post)
		return RollbackResult{Complete: reason == "", BaselineRestored: reason == "", Incomplete: reason != "", Reason: reason}
	}
	postBase := CaptureBaseline(post)
	if regressed, field := DataplaneRegressed(pre, postBase); regressed {
		return RollbackResult{
			Incomplete: true,
			Reason:     "rollback worse than baseline: " + field,
		}
	}
	// Same preexisting CP disconnect is OK.
	if pre.CPConnected && !postBase.CPConnected {
		return RollbackResult{
			Incomplete: true,
			Reason:     "rollback lost cp_connected",
		}
	}
	if !postBase.DataplaneOK && pre.DataplaneOK {
		return RollbackResult{
			Incomplete: true,
			Reason:     "rollback dataplane unhealthy",
		}
	}
	return RollbackResult{
		Complete:         true,
		BaselineRestored: true,
		Reason:           "baseline restored (global healthy may remain false for preexisting CP reasons)",
	}
}

// IntentionallyStopped requires explicit lifecycle evidence, never accepting=false alone.
func (b Baseline) IntentionallyStopped() bool {
	return b.LifecycleKnown && !b.Accepting && (b.Draining || b.MaintenanceMode)
}

func stoppedUpdateFailure(pre Baseline, post Status) string {
	if post.Accepting || post.Draining != pre.Draining || post.MaintenanceMode != pre.MaintenanceMode {
		return "update lifecycle changed"
	}
	if !post.Running || !post.IdentityPresent || post.VersionBlocked || !post.TUNReady || !post.BridgeOK || post.SkipTUN {
		return "stopped node runtime/identity/TUN/bridge not ready"
	}
	if !post.TicketKeysLoaded || post.RevocationStale {
		return "stopped node ticket keys/revocation not ready"
	}
	if pre.CPConnected && !post.CPConnected {
		return "cp_connected regressed"
	}
	if pre.NodeID != "" && pre.NodeID != post.NodeID {
		return "node identity changed"
	}
	if post.ConfigVersion < pre.ConfigVersion {
		return "applied configuration regressed"
	}
	return ""
}
