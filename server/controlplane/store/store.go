package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyxveil/nvp/controlplane/model"
	_ "modernc.org/sqlite"
)

// Store provides persistent Control Plane storage.
type Store struct {
	db *sql.DB
}

// Open opens or creates SQLite database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS licenses (
  license_id TEXT PRIMARY KEY,
  plan TEXT NOT NULL,
  max_devices INTEGER NOT NULL DEFAULT 3,
  enabled INTEGER NOT NULL DEFAULT 1,
  revoked INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  locations_json TEXT
);
CREATE TABLE IF NOT EXISTS devices (
  device_id TEXT PRIMARY KEY,
  license_id TEXT NOT NULL,
  public_key BLOB,
  enabled INTEGER NOT NULL DEFAULT 1,
  revoked INTEGER NOT NULL DEFAULT 0,
  registered_at TEXT NOT NULL,
  last_seen TEXT
);
CREATE TABLE IF NOT EXISTS nodes (
  node_id TEXT PRIMARY KEY,
  location_id TEXT NOT NULL,
  country TEXT,
  city TEXT,
  display_name TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  test_only INTEGER NOT NULL DEFAULT 0,
  draining INTEGER NOT NULL DEFAULT 0,
  endpoints_json TEXT,
  capacity INTEGER DEFAULT 100,
  current_sessions INTEGER DEFAULT 0,
  health_json TEXT,
  last_seen TEXT
);
CREATE TABLE IF NOT EXISTS revocations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL,
  ref_id TEXT NOT NULL,
  revoked_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS node_identities (
  node_id TEXT PRIMARY KEY,
  public_key BLOB NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1
);
`
	_, err := s.db.Exec(schema)
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// UpsertLicense stores license record.
func (s *Store) UpsertLicense(ctx context.Context, lic model.LicenseRecord) error {
	loc, _ := json.Marshal(lic.Locations)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO licenses (license_id, plan, max_devices, enabled, revoked, expires_at, locations_json)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(license_id) DO UPDATE SET
  plan=excluded.plan, max_devices=excluded.max_devices, enabled=excluded.enabled,
  revoked=excluded.revoked, expires_at=excluded.expires_at, locations_json=excluded.locations_json
`, lic.LicenseID, lic.Plan, lic.MaxDevices, boolInt(lic.Enabled), boolInt(lic.Revoked),
		lic.ExpiresAt.UTC().Format(time.RFC3339), string(loc))
	return err
}

// GetLicense returns license by ID.
func (s *Store) GetLicense(ctx context.Context, licenseID string) (*model.LicenseRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT license_id, plan, max_devices, enabled, revoked, expires_at, locations_json
FROM licenses WHERE license_id = ?`, licenseID)
	var lic model.LicenseRecord
	var enabled, revoked int
	var expires, locJSON string
	if err := row.Scan(&lic.LicenseID, &lic.Plan, &lic.MaxDevices, &enabled, &revoked, &expires, &locJSON); err != nil {
		return nil, err
	}
	lic.Enabled = enabled == 1
	lic.Revoked = revoked == 1
	lic.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
	_ = json.Unmarshal([]byte(locJSON), &lic.Locations)
	return &lic, nil
}

// RegisterDevice registers a device.
func (s *Store) RegisterDevice(ctx context.Context, dev model.DeviceRecord) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO devices (device_id, license_id, public_key, enabled, revoked, registered_at, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(device_id) DO UPDATE SET
  license_id=excluded.license_id, public_key=excluded.public_key,
  enabled=excluded.enabled, revoked=excluded.revoked, last_seen=excluded.last_seen
`, dev.DeviceID, dev.LicenseID, dev.PublicKey, boolInt(dev.Enabled), boolInt(dev.Revoked),
		dev.Registered.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	return err
}

// GetDevice returns device record.
func (s *Store) GetDevice(ctx context.Context, deviceID string) (*model.DeviceRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT device_id, license_id, public_key, enabled, revoked, registered_at FROM devices WHERE device_id=?`, deviceID)
	var dev model.DeviceRecord
	var enabled, revoked int
	var reg string
	if err := row.Scan(&dev.DeviceID, &dev.LicenseID, &dev.PublicKey, &enabled, &revoked, &reg); err != nil {
		return nil, err
	}
	dev.Enabled = enabled == 1
	dev.Revoked = revoked == 1
	dev.Registered, _ = time.Parse(time.RFC3339, reg)
	return &dev, nil
}

// CountDevices returns active device count for license.
func (s *Store) CountDevices(ctx context.Context, licenseID string) (int, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM devices WHERE license_id=? AND revoked=0 AND enabled=1`, licenseID)
	var n int
	return n, row.Scan(&n)
}

// UpsertNode stores node registry entry.
func (s *Store) UpsertNode(ctx context.Context, n model.NodeRegistryEntry) error {
	eps, _ := json.Marshal(n.Endpoints)
	health, _ := json.Marshal(n.Health)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO nodes (node_id, location_id, country, city, display_name, enabled, test_only, draining,
  endpoints_json, capacity, current_sessions, health_json, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id) DO UPDATE SET
  location_id=excluded.location_id, country=excluded.country, city=excluded.city,
  display_name=excluded.display_name, enabled=excluded.enabled, test_only=excluded.test_only,
  draining=excluded.draining, endpoints_json=excluded.endpoints_json, capacity=excluded.capacity,
  current_sessions=excluded.current_sessions, health_json=excluded.health_json, last_seen=excluded.last_seen
`, n.NodeID, n.LocationID, n.Country, n.City, n.DisplayName, boolInt(n.Enabled), boolInt(n.TestOnly),
		boolInt(n.Draining), string(eps), n.Capacity, n.CurrentSessions, string(health),
		n.LastSeen.UTC().Format(time.RFC3339))
	return err
}

// ListNodes returns all nodes.
func (s *Store) ListNodes(ctx context.Context) ([]model.NodeRegistryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT node_id, location_id, country, city, display_name, enabled, test_only, draining,
  endpoints_json, capacity, current_sessions, health_json, last_seen FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NodeRegistryEntry
	for rows.Next() {
		var n model.NodeRegistryEntry
		var enabled, testOnly, draining int
		var epsJSON, healthJSON, lastSeen string
		if err := rows.Scan(&n.NodeID, &n.LocationID, &n.Country, &n.City, &n.DisplayName,
			&enabled, &testOnly, &draining, &epsJSON, &n.Capacity, &n.CurrentSessions, &healthJSON, &lastSeen); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		n.TestOnly = testOnly == 1
		n.Draining = draining == 1
		_ = json.Unmarshal([]byte(epsJSON), &n.Endpoints)
		_ = json.Unmarshal([]byte(healthJSON), &n.Health)
		n.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		out = append(out, n)
	}
	return out, nil
}

// Revoke adds revocation entry.
func (s *Store) Revoke(ctx context.Context, kind, refID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO revocations (kind, ref_id, revoked_at) VALUES (?, ?, ?)`,
		kind, refID, time.Now().UTC().Format(time.RFC3339))
	return err
}

// IsRevoked checks revocation table.
func (s *Store) IsRevoked(ctx context.Context, kind, refID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revocations WHERE kind=? AND ref_id=?`, kind, refID)
	var n int
	return n > 0, row.Scan(&n)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ValidateNodeToken verifies node has registered identity (simplified).
func (s *Store) ValidateNodeToken(ctx context.Context, nodeID string) error {
	row := s.db.QueryRowContext(ctx, `SELECT enabled FROM node_identities WHERE node_id=?`, nodeID)
	var enabled int
	if err := row.Scan(&enabled); err != nil {
		return fmt.Errorf("unknown node: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("node disabled")
	}
	return nil
}

// RegisterNodeIdentity registers node service public key.
func (s *Store) RegisterNodeIdentity(ctx context.Context, nodeID string, pubKey []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO node_identities (node_id, public_key, enabled) VALUES (?, ?, 1)
ON CONFLICT(node_id) DO UPDATE SET public_key=excluded.public_key, enabled=1`, nodeID, pubKey)
	return err
}
