package revocation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
)

// SyncCache polls Control Plane revocation list and implements ticket.RevocationCache.
type SyncCache struct {
	mu        sync.RWMutex
	inner     *ticket.MemoryRevocation
	updatedAt int64
	cpURL     string
	client    *http.Client
	interval  time.Duration
}

// NewSyncCache creates a revocation cache synced from Control Plane.
func NewSyncCache(cpBaseURL string) *SyncCache {
	return &SyncCache{
		inner:    ticket.NewMemoryRevocation(),
		cpURL:    cpBaseURL,
		client:   &http.Client{Timeout: 10 * time.Second},
		interval: 60 * time.Second,
	}
}

// IsRevoked implements ticket.RevocationCache.
func (c *SyncCache) IsRevoked(jti, licenseID, deviceID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.inner.IsRevoked(jti, licenseID, deviceID)
}

// UpdatedAt returns last successful sync timestamp.
func (c *SyncCache) UpdatedAt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.updatedAt
}

// Sync pulls latest revocation list from Control Plane.
func (c *SyncCache) Sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cpURL+"/api/v1/revocation", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation sync: status %d", resp.StatusCode)
	}
	var list api.RevocationListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return err
	}
	next := ticket.NewMemoryRevocation()
	for _, j := range list.RevokedJTIs {
		next.RevokeJTI(j)
	}
	for _, l := range list.RevokedLicenses {
		next.RevokeLicense(l)
	}
	for _, d := range list.RevokedDevices {
		next.RevokeDevice(d)
	}
	c.mu.Lock()
	c.inner = next
	c.updatedAt = list.UpdatedAt
	c.mu.Unlock()
	return nil
}

// RunLoop periodically syncs until context cancelled.
func (c *SyncCache) RunLoop(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		_ = c.Sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
