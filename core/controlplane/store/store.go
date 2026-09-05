package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/nyxveil/nvp/core/controlplane/model"
	"github.com/nyxveil/nvp/core/node"
	"golang.org/x/crypto/chacha20poly1305"
	_ "modernc.org/sqlite"
)

// Store provides persistent Control Plane storage.
type Store struct {
	db         *sql.DB
	kek        []byte
	requireKEK bool // production: refuse plaintext secret storage
}

// Open opens or creates SQLite database.
// Empty/invalid NVP_LICENSE_KEK keeps secrets in plaintext (dev only).
func Open(path string) (*Store, error) {
	return OpenWithKEK(path, os.Getenv("NVP_LICENSE_KEK"))
}

// OpenProduction opens SQLite and REQUIRES a valid 32-byte KEK from NVP_LICENSE_KEK.
func OpenProduction(path string) (*Store, error) {
	kekHex := os.Getenv("NVP_LICENSE_KEK")
	kek := parseLicenseKEK(kekHex)
	if len(kek) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("NVP_LICENSE_KEK must be a valid 32-byte key (64 hex characters); refusing plaintext secrets")
	}
	s, err := OpenWithKEK(path, kekHex)
	if err != nil {
		return nil, err
	}
	s.requireKEK = true
	return s, nil
}

// OpenWithKEK opens the database and optionally encrypts license secrets at rest.
// kekHex should be 64 hex characters (32-byte ChaCha20 key). Empty keeps secrets in plaintext
// unless OpenProduction set requireKEK.
func OpenWithKEK(path, kekHex string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	_, _ = db.Exec(`PRAGMA journal_mode=DELETE`)
	_, _ = db.Exec(`PRAGMA foreign_keys=ON`)
	s := &Store{db: db, kek: parseLicenseKEK(kekHex)}
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
  locations_json TEXT,
  secret TEXT NOT NULL DEFAULT ''
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
  last_seen TEXT,
  server_name TEXT NOT NULL DEFAULT '',
  spki_pin BLOB,
  protocol_version INTEGER NOT NULL DEFAULT 0,
  server_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT ''
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
  enabled INTEGER NOT NULL DEFAULT 1,
  last_token_ts INTEGER NOT NULL DEFAULT 0
);
`
	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(`ALTER TABLE licenses ADD COLUMN secret TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE node_identities ADD COLUMN last_token_ts INTEGER NOT NULL DEFAULT 0`)
	// Node registry columns (idempotent for existing DBs).
	_, _ = s.db.Exec(`ALTER TABLE nodes ADD COLUMN server_name TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE nodes ADD COLUMN spki_pin BLOB`)
	_, _ = s.db.Exec(`ALTER TABLE nodes ADD COLUMN protocol_version INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.Exec(`ALTER TABLE nodes ADD COLUMN server_version TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE nodes ADD COLUMN status TEXT NOT NULL DEFAULT ''`)
	return nil
}

// Close closes the database after a best-effort checkpoint so Windows can
// remove temporary DB directories during tests.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	err := s.db.Close()
	s.db = nil
	return err
}

// UpsertLicense stores license record.
func (s *Store) UpsertLicense(ctx context.Context, lic model.LicenseRecord) error {
	loc, _ := json.Marshal(lic.Locations)
	secret, err := s.wrapSecret(lic.Secret)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO licenses (license_id, plan, max_devices, enabled, revoked, expires_at, locations_json, secret)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(license_id) DO UPDATE SET
  plan=excluded.plan, max_devices=excluded.max_devices, enabled=excluded.enabled,
  revoked=excluded.revoked, expires_at=excluded.expires_at, locations_json=excluded.locations_json,
  secret=excluded.secret
`, lic.LicenseID, lic.Plan, lic.MaxDevices, boolInt(lic.Enabled), boolInt(lic.Revoked),
		lic.ExpiresAt.UTC().Format(time.RFC3339), string(loc), secret)
	return err
}

// GetLicense returns license by ID.
func (s *Store) GetLicense(ctx context.Context, licenseID string) (*model.LicenseRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT license_id, plan, max_devices, enabled, revoked, expires_at, locations_json, secret
FROM licenses WHERE license_id = ?`, licenseID)
	var lic model.LicenseRecord
	var enabled, revoked int
	var expires, locJSON, secret string
	if err := row.Scan(&lic.LicenseID, &lic.Plan, &lic.MaxDevices, &enabled, &revoked, &expires, &locJSON, &secret); err != nil {
		return nil, err
	}
	lic.Enabled = enabled == 1
	lic.Revoked = revoked == 1
	lic.ExpiresAt, _ = time.Parse(time.RFC3339, expires)
	_ = json.Unmarshal([]byte(locJSON), &lic.Locations)
	secretPlain, err := s.unwrapSecret(secret)
	if err != nil {
		return nil, err
	}
	lic.Secret = secretPlain
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

// UpsertNode stores a full node registry entry (static config + health).
// Prefer CreateOrUpdateNodeConfig for admin writes and UpdateNodeHealth for heartbeats.
func (s *Store) UpsertNode(ctx context.Context, n model.NodeRegistryEntry) error {
	return s.CreateOrUpdateNodeConfig(ctx, n)
}

// CreateOrUpdateNodeConfig INSERT/UPDATEs ALL static node fields including identity/SPKI.
func (s *Store) CreateOrUpdateNodeConfig(ctx context.Context, n model.NodeRegistryEntry) error {
	eps, _ := json.Marshal(n.Endpoints)
	health, _ := json.Marshal(n.Health)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO nodes (
  node_id, location_id, country, city, display_name, enabled, test_only, draining,
  endpoints_json, capacity, current_sessions, health_json, last_seen,
  server_name, spki_pin, protocol_version, server_version, status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id) DO UPDATE SET
  location_id=excluded.location_id, country=excluded.country, city=excluded.city,
  display_name=excluded.display_name, enabled=excluded.enabled, test_only=excluded.test_only,
  draining=excluded.draining, endpoints_json=excluded.endpoints_json, capacity=excluded.capacity,
  current_sessions=excluded.current_sessions, health_json=excluded.health_json, last_seen=excluded.last_seen,
  server_name=excluded.server_name, spki_pin=excluded.spki_pin,
  protocol_version=excluded.protocol_version, server_version=excluded.server_version,
  status=excluded.status
`, n.NodeID, n.LocationID, n.Country, n.City, n.DisplayName, boolInt(n.Enabled), boolInt(n.TestOnly),
		boolInt(n.Draining), string(eps), n.Capacity, n.CurrentSessions, string(health),
		n.LastSeen.UTC().Format(time.RFC3339), n.ServerName, n.SPKIPin, int(n.ProtocolVersion),
		n.ServerVersion, string(n.Status))
	return err
}

// UpdateNodeHealth updates ONLY dynamic health fields — never static node configuration.
// If capacity < 0, capacity is left unchanged.
func (s *Store) UpdateNodeHealth(ctx context.Context, nodeID string, health model.NodeHealth, sessions, capacity int, lastSeen time.Time) error {
	healthJSON, _ := json.Marshal(health)
	last := lastSeen.UTC().Format(time.RFC3339)
	var res sql.Result
	var err error
	if capacity < 0 {
		res, err = s.db.ExecContext(ctx, `
UPDATE nodes SET health_json=?, current_sessions=?, last_seen=? WHERE node_id=?`,
			string(healthJSON), sessions, last, nodeID)
	} else {
		res, err = s.db.ExecContext(ctx, `
UPDATE nodes SET health_json=?, current_sessions=?, capacity=?, last_seen=? WHERE node_id=?`,
			string(healthJSON), sessions, capacity, last, nodeID)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("node not found")
	}
	return nil
}

const nodeSelectCols = `
node_id, location_id, country, city, display_name, enabled, test_only, draining,
endpoints_json, capacity, current_sessions, health_json, last_seen,
server_name, spki_pin, protocol_version, server_version, status`

// ListNodes returns all nodes with full static + health fields.
func (s *Store) ListNodes(ctx context.Context) ([]model.NodeRegistryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeSelectCols+` FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NodeRegistryEntry
	for rows.Next() {
		n, err := scanNodeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetNode returns a single node by ID.
func (s *Store) GetNode(ctx context.Context, nodeID string) (*model.NodeRegistryEntry, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeSelectCols+` FROM nodes WHERE node_id=?`, nodeID)
	n, err := scanNodeRow(row)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanNodeRow(row scannable) (model.NodeRegistryEntry, error) {
	var n model.NodeRegistryEntry
	var enabled, testOnly, draining int
	var epsJSON, healthJSON, lastSeen sql.NullString
	var serverName, serverVersion, status sql.NullString
	var spki []byte
	var protoVer int
	if err := row.Scan(&n.NodeID, &n.LocationID, &n.Country, &n.City, &n.DisplayName,
		&enabled, &testOnly, &draining, &epsJSON, &n.Capacity, &n.CurrentSessions, &healthJSON, &lastSeen,
		&serverName, &spki, &protoVer, &serverVersion, &status); err != nil {
		return n, err
	}
	n.Enabled = enabled == 1
	n.TestOnly = testOnly == 1
	n.Draining = draining == 1
	if epsJSON.Valid && epsJSON.String != "" {
		_ = json.Unmarshal([]byte(epsJSON.String), &n.Endpoints)
	}
	if healthJSON.Valid && healthJSON.String != "" {
		_ = json.Unmarshal([]byte(healthJSON.String), &n.Health)
	}
	if lastSeen.Valid && lastSeen.String != "" {
		n.LastSeen, _ = time.Parse(time.RFC3339, lastSeen.String)
	}
	n.ServerName = serverName.String
	if len(spki) > 0 {
		n.SPKIPin = append([]byte(nil), spki...)
	}
	n.ProtocolVersion = uint16(protoVer)
	n.ServerVersion = serverVersion.String
	n.Status = node.Status(status.String)
	return n, nil
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

// RegisterNodeIdentity registers node service public key.
func (s *Store) RegisterNodeIdentity(ctx context.Context, nodeID string, pubKey []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO node_identities (node_id, public_key, enabled) VALUES (?, ?, 1)
ON CONFLICT(node_id) DO UPDATE SET public_key=excluded.public_key, enabled=1`, nodeID, pubKey)
	return err
}
