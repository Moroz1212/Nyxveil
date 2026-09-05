// Package revocation maintains a local Control Plane revocation snapshot.
package revocation

import (
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/server/internal/controlplane"
)

// MaxStaleAge is the maximum age of a successful sync before fail-closed.
const MaxStaleAge = 24 * time.Hour

// Compile-time check: Cache implements ticket.RevocationCache.
var _ ticket.RevocationCache = (*Cache)(nil)

// Cache is an in-memory JTI/license/device revocation cache with stale policy.
// After MaxStaleAge without a successful Apply, IsRevoked returns true (fail-closed)
// so new sessions cannot authenticate on a stale deny-list.
type Cache struct {
	mu          sync.RWMutex
	jtis        map[string]struct{}
	licenses    map[string]struct{}
	devices     map[string]struct{}
	syncedAt    time.Time // wall time of last successful Apply
	snapshotAt  int64     // CP UpdatedAt from last snapshot
	maxStale    time.Duration
	hasSnapshot bool
}

// New returns an empty cache. Until the first successful Apply, IsRevoked
// fail-closes (treats everything as revoked) to avoid accepting sessions
// before revocation data is available.
func New() *Cache {
	return &Cache{
		jtis:     make(map[string]struct{}),
		licenses: make(map[string]struct{}),
		devices:  make(map[string]struct{}),
		maxStale: MaxStaleAge,
	}
}

// SetMaxStale overrides the stale age (tests).
func (c *Cache) SetMaxStale(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxStale = d
}

// Apply replaces the cache from a Control Plane snapshot.
func (c *Cache) Apply(snap controlplane.RevocationSnapshot) {
	jtis := make(map[string]struct{}, len(snap.RevokedJTIs))
	for _, id := range snap.RevokedJTIs {
		if id != "" {
			jtis[id] = struct{}{}
		}
	}
	licenses := make(map[string]struct{}, len(snap.RevokedLicenses))
	for _, id := range snap.RevokedLicenses {
		if id != "" {
			licenses[id] = struct{}{}
		}
	}
	devices := make(map[string]struct{}, len(snap.RevokedDevices))
	for _, id := range snap.RevokedDevices {
		if id != "" {
			devices[id] = struct{}{}
		}
	}
	c.mu.Lock()
	c.jtis = jtis
	c.licenses = licenses
	c.devices = devices
	c.syncedAt = time.Now()
	c.snapshotAt = snap.UpdatedAt
	c.hasSnapshot = true
	c.mu.Unlock()
}

// IsRevoked implements ticket.RevocationCache.
func (c *Cache) IsRevoked(jti, licenseID, deviceID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.hasSnapshot || c.isStaleLocked() {
		return true // fail-closed for new sessions
	}
	if jti != "" {
		if _, ok := c.jtis[jti]; ok {
			return true
		}
	}
	if licenseID != "" {
		if _, ok := c.licenses[licenseID]; ok {
			return true
		}
	}
	if deviceID != "" {
		if _, ok := c.devices[deviceID]; ok {
			return true
		}
	}
	return false
}

// Stale reports whether the cache is past MaxStaleAge or has never synced.
func (c *Cache) Stale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.hasSnapshot || c.isStaleLocked()
}

func (c *Cache) isStaleLocked() bool {
	age := c.maxStale
	if age <= 0 {
		age = MaxStaleAge
	}
	return time.Since(c.syncedAt) > age
}

// SyncedAt returns the local wall time of the last successful Apply.
func (c *Cache) SyncedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.syncedAt
}

// SnapshotUpdatedAt returns the Control Plane UpdatedAt from the last Apply.
func (c *Cache) SnapshotUpdatedAt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshotAt
}
