package liveness

import (
	"context"
	"sync"
	"time"

	"github.com/neilberkman/ccrider/internal/core/db"
)

// Cache serves liveness snapshots with a short TTL so a burst of requests
// (an MCP handler annotating results plus a list_open_sessions call) shares
// one process scan. A failed scan is returned as-is and never cached, so
// callers can treat errors as "liveness unknown".
type Cache struct {
	Source Source
	TTL    time.Duration

	mu       sync.Mutex
	scanned  time.Time
	snapshot []LiveSession
}

// DefaultTTL keeps repeat scans within one interactive exchange free while
// staying fresh enough that a closed window disappears promptly.
const DefaultTTL = 5 * time.Second

// Snapshot returns the current live sessions, rescanning at most once per TTL.
func (c *Cache) Snapshot(ctx context.Context, database *db.DB) ([]LiveSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if !c.scanned.IsZero() && time.Since(c.scanned) < ttl {
		return c.snapshot, nil
	}

	src := c.Source
	if src == nil {
		src = SystemSource{}
	}
	live, err := Scan(ctx, src, database)
	if err != nil {
		return nil, err
	}
	c.scanned = time.Now()
	c.snapshot = live
	return live, nil
}

// BySessionID indexes a snapshot for annotation lookups. Unknown-match rows
// (no session id) are absent by construction.
func BySessionID(live []LiveSession) map[string]LiveSession {
	index := make(map[string]LiveSession, len(live))
	for _, l := range live {
		if l.SessionID != "" {
			index[l.SessionID] = l
		}
	}
	return index
}
