package conversation

import (
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

const (
	temporaryContextObservationTTL   = 30 * time.Minute
	temporaryContextObservationLimit = 1024
)

type temporaryContextObservationKey struct {
	userID   uint
	session  string
	route    string
	ancestry string
}

type temporaryContextObservationEntry struct {
	value     contextInputObservation
	expiresAt time.Time
}

// Only fingerprints, usage numbers and expiry times are retained. Losing this
// optional process-local cache (restart, eviction, another replica) only removes
// calibration; the same conservative estimate and budget still apply.
type temporaryContextObservationCache struct {
	mu      sync.Mutex
	entries map[temporaryContextObservationKey]temporaryContextObservationEntry
}

func (c *temporaryContextObservationCache) ancestors(scope temporaryContextObservationKey, messages []llm.Message, now time.Time) (string, []contextInputObservation) {
	var ancestorKeys []temporaryContextObservationKey
	for _, message := range messages {
		scope.ancestry = extendTemporaryContextAncestry(scope.ancestry, message)
		if message.Role == "assistant" {
			ancestorKeys = append(ancestorKeys, scope)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	var observations []contextInputObservation
	for index := len(ancestorKeys) - 1; index >= 0; index-- {
		key := ancestorKeys[index]
		if entry, exists := c.entries[key]; exists {
			if !now.Before(entry.expiresAt) {
				delete(c.entries, key)
				continue
			}
			observations = append(observations, entry.value)
		}
	}
	return scope.ancestry, observations
}

func (c *temporaryContextObservationCache) put(key temporaryContextObservationKey, observation contextInputObservation, now time.Time) {
	if observation.InputTokens <= 0 || observation.StableSignature == "" || key.ancestry == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[temporaryContextObservationKey]temporaryContextObservationEntry)
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= temporaryContextObservationLimit {
		var oldestKey temporaryContextObservationKey
		var oldest time.Time
		for existingKey, entry := range c.entries {
			if oldest.IsZero() || entry.expiresAt.Before(oldest) {
				oldestKey, oldest = existingKey, entry.expiresAt
			}
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = temporaryContextObservationEntry{value: observation, expiresAt: now.Add(temporaryContextObservationTTL)}
}

// Hash the pre-prompt messages (including the attachment in its original turn),
// rather than the final layout that may move a part or inject system text.
func extendTemporaryContextAncestry(prefix string, message llm.Message) string {
	type partFingerprint struct {
		Kind, Text, MIME, Name string
		SourceFileIDs          []string
		Bytes                  [sha256.Size]byte
	}
	var parts []partFingerprint
	for _, part := range message.Parts {
		parts = append(parts, partFingerprint{
			Kind: part.Kind, Text: part.Text, MIME: part.MimeType, Name: part.FileName,
			SourceFileIDs: part.SourceFileIDs, Bytes: sha256.Sum256(part.Data),
		})
	}
	return contextObservationHash(struct {
		Prefix, Role, Content string
		Parts                 []partFingerprint
	}{prefix, message.Role, strings.TrimSpace(message.Content), parts})
}
