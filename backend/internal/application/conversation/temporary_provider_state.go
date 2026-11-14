package conversation

import (
	"sync"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type temporaryProviderStateEntry struct {
	value   string
	expires time.Time
}
type temporaryProviderStateCache struct {
	mu      sync.Mutex
	entries map[temporaryContextObservationKey]temporaryProviderStateEntry
	bytes   int
}

func (c *temporaryProviderStateCache) restore(scope temporaryContextObservationKey, messages []llm.Message, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range messages {
		scope.ancestry = extendTemporaryContextAncestry(scope.ancestry, messages[i])
		if messages[i].Role != "assistant" {
			continue
		}
		if entry, ok := c.entries[scope]; ok {
			if now.Before(entry.expires) {
				messages[i].ProviderStateJSON = entry.value
			} else {
				c.bytes -= len(entry.value)
				delete(c.entries, scope)
			}
		}
	}
}

func (c *temporaryProviderStateCache) put(scope temporaryContextObservationKey, state string, now time.Time) {
	if state == "" || len(state) > 8*1024*1024 || scope.userID == 0 || scope.session == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[temporaryContextObservationKey]temporaryProviderStateEntry{}
	}
	if old, ok := c.entries[scope]; ok {
		c.bytes -= len(old.value)
		delete(c.entries, scope)
	}
	for key, old := range c.entries {
		if !now.Before(old.expires) {
			c.bytes -= len(old.value)
			delete(c.entries, key)
		}
	}
	for len(c.entries) >= 128 || c.bytes+len(state) > 16*1024*1024 {
		var oldestKey temporaryContextObservationKey
		var oldest time.Time
		for key, old := range c.entries {
			if oldest.IsZero() || old.expires.Before(oldest) {
				oldestKey, oldest = key, old.expires
			}
		}
		old := c.entries[oldestKey]
		c.bytes -= len(old.value)
		delete(c.entries, oldestKey)
	}
	c.entries[scope] = temporaryProviderStateEntry{value: state, expires: now.Add(30 * time.Minute)}
	c.bytes += len(state)
}
