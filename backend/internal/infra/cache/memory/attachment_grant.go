package memory

import (
	"context"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

func NewAttachmentGrants(c *Cache) repository.AttachmentGrantRepository { return c }

func (c *Cache) PutAttachmentGrant(_ context.Context, hash string, grant repository.AttachmentGrant, ttl time.Duration) error {
	if hash == "" || ttl <= 0 || grant.RunID == "" {
		return repository.ErrInvalidInput
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.maybeSweepLocked(now)
	if expiry, revoked := c.attachmentRevokedRuns[grant.RunID]; revoked && now.Before(expiry) {
		return repository.ErrNotFound
	}
	if c.attachmentGrants == nil {
		c.attachmentGrants = make(map[string]repository.AttachmentGrant)
	}
	deadline := now.Add(ttl)
	if grant.ExpiresAt.IsZero() || grant.ExpiresAt.After(deadline) {
		grant.ExpiresAt = deadline
	}
	if c.attachmentRunGrants == nil {
		c.attachmentRunGrants = make(map[string]map[string]struct{})
	}
	c.removeAttachmentGrantLocked(hash)
	if c.attachmentRunGrants[grant.RunID] == nil {
		c.attachmentRunGrants[grant.RunID] = make(map[string]struct{})
	}
	c.attachmentRunGrants[grant.RunID][hash] = struct{}{}
	c.attachmentGrants[hash] = grant
	return nil
}

func (c *Cache) GetAttachmentGrant(_ context.Context, hash string) (*repository.AttachmentGrant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maybeSweepLocked(time.Now())
	g, ok := c.attachmentGrants[hash]
	if !ok || !time.Now().Before(g.ExpiresAt) {
		c.removeAttachmentGrantLocked(hash)
		return nil, repository.ErrNotFound
	}
	return &g, nil
}

func (c *Cache) RevokeAttachmentRun(_ context.Context, run string) ([]repository.AttachmentGrant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maybeSweepLocked(time.Now())
	if c.attachmentRevokedRuns == nil {
		c.attachmentRevokedRuns = make(map[string]time.Time)
	}
	c.attachmentRevokedRuns[run] = time.Now().Add(repository.AttachmentGrantMaxTTL)
	var revoked []repository.AttachmentGrant
	for key := range c.attachmentRunGrants[run] {
		if g, ok := c.attachmentGrants[key]; ok {
			revoked = append(revoked, g)
		}
		c.removeAttachmentGrantLocked(key)
	}
	return revoked, nil
}

func (c *Cache) removeAttachmentGrantLocked(hash string) {
	if grant, ok := c.attachmentGrants[hash]; ok {
		delete(c.attachmentRunGrants[grant.RunID], hash)
		if len(c.attachmentRunGrants[grant.RunID]) == 0 {
			delete(c.attachmentRunGrants, grant.RunID)
		}
	}
	delete(c.attachmentGrants, hash)
}
