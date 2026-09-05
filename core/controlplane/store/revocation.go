package store

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/nyxveil/nvp/core/auth/nodeauth"
	"github.com/nyxveil/nvp/core/controlplane/api"
)

// RevocationEntry is a single revocation record.
type RevocationEntry struct {
	Kind string
	Ref  string
}

// ListRevocations returns all revocation entries from DB and revoked flags.
func (s *Store) ListRevocations(ctx context.Context) (api.RevocationListResponse, error) {
	resp := api.RevocationListResponse{UpdatedAt: time.Now().Unix()}

	rows, err := s.db.QueryContext(ctx, `SELECT kind, ref_id FROM revocations ORDER BY id`)
	if err != nil {
		return resp, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, ref string
		if err := rows.Scan(&kind, &ref); err != nil {
			return resp, err
		}
		switch kind {
		case "jti":
			resp.RevokedJTIs = append(resp.RevokedJTIs, ref)
		case "license":
			resp.RevokedLicenses = append(resp.RevokedLicenses, ref)
		case "device":
			resp.RevokedDevices = append(resp.RevokedDevices, ref)
		}
	}

	licRows, err := s.db.QueryContext(ctx, `SELECT license_id FROM licenses WHERE revoked=1`)
	if err != nil {
		return resp, err
	}
	defer licRows.Close()
	for licRows.Next() {
		var id string
		if err := licRows.Scan(&id); err != nil {
			return resp, err
		}
		resp.RevokedLicenses = appendUnique(resp.RevokedLicenses, id)
	}

	devRows, err := s.db.QueryContext(ctx, `SELECT device_id FROM devices WHERE revoked=1`)
	if err != nil {
		return resp, err
	}
	defer devRows.Close()
	for devRows.Next() {
		var id string
		if err := devRows.Scan(&id); err != nil {
			return resp, err
		}
		resp.RevokedDevices = appendUnique(resp.RevokedDevices, id)
	}
	return resp, nil
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// RevokeDevice marks device revoked in devices table and revocations log.
func (s *Store) RevokeDevice(ctx context.Context, deviceID string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE devices SET revoked=1, enabled=0 WHERE device_id=?`, deviceID); err != nil {
		return err
	}
	return s.Revoke(ctx, "device", deviceID)
}

// GetNodePublicKey returns registered node Ed25519 public key.
func (s *Store) GetNodePublicKey(ctx context.Context, nodeID string) (ed25519.PublicKey, error) {
	row := s.db.QueryRowContext(ctx, `SELECT public_key, enabled FROM node_identities WHERE node_id=?`, nodeID)
	var raw []byte
	var enabled int
	if err := row.Scan(&raw, &enabled); err != nil {
		return nil, fmt.Errorf("unknown node: %w", err)
	}
	if enabled != 1 {
		return nil, fmt.Errorf("node disabled")
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid node public key length")
	}
	return ed25519.PublicKey(raw), nil
}

// ValidateNodeToken verifies node heartbeat token signature.
func (s *Store) ValidateNodeToken(ctx context.Context, nodeID, token string) error {
	pub, err := s.GetNodePublicKey(ctx, nodeID)
	if err != nil {
		return err
	}
	if err := nodeauth.VerifyToken(nodeID, token, pub, time.Now()); err != nil {
		return err
	}
	ts, err := nodeauth.TokenUnix(token)
	if err != nil {
		return err
	}
	row := s.db.QueryRowContext(ctx, `SELECT last_token_ts FROM node_identities WHERE node_id=?`, nodeID)
	var last int64
	if err := row.Scan(&last); err != nil {
		return err
	}
	if ts <= last {
		return nodeauth.ErrReplayNodeToken
	}
	_, err = s.db.ExecContext(ctx, `UPDATE node_identities SET last_token_ts=? WHERE node_id=?`, ts, nodeID)
	return err
}

// SetNodeDrain updates node draining flag.
func (s *Store) SetNodeDrain(ctx context.Context, nodeID string, draining bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE nodes SET draining=? WHERE node_id=?`, boolInt(draining), nodeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("node not found")
	}
	return nil
}

// SetNodeMaintenance toggles node enabled flag.
func (s *Store) SetNodeMaintenance(ctx context.Context, nodeID string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE nodes SET enabled=? WHERE node_id=?`, boolInt(enabled), nodeID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("node not found")
	}
	return nil
}
