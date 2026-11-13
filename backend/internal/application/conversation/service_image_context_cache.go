package conversation

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
)

const (
	maxPreparedConversationImageCacheEntries = 64
	maxPreparedConversationImageCacheBytes   = 64 * 1024 * 1024
)

type preparedConversationImage struct {
	data     []byte
	mimeType string
}

type preparedConversationImageCacheEntry struct {
	key   string
	image preparedConversationImage
}

type preparedConversationImageCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	recency    *list.List
	totalBytes int
	maxEntries int
	maxBytes   int
	inflight   map[string]*preparedImageFlight
}

type preparedImageFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	image   preparedConversationImage
	err     error
}

func newPreparedConversationImageCache(maxEntries int, maxBytes int) *preparedConversationImageCache {
	return &preparedConversationImageCache{
		entries:    make(map[string]*list.Element),
		recency:    list.New(),
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		inflight:   make(map[string]*preparedImageFlight),
	}
}

func defaultPreparedConversationImageCache() *preparedConversationImageCache {
	return newPreparedConversationImageCache(
		maxPreparedConversationImageCacheEntries,
		maxPreparedConversationImageCacheBytes,
	)
}

func (s *Service) conversationImageCache(cfg config.Config) *preparedConversationImageCache {
	cache := s.imageContextCache
	if cache == nil {
		return newPreparedConversationImageCache(cfg.PreparedImageCacheMaxEntries, int(cfg.PreparedImageCacheMaxBytes))
	}
	cache.configure(cfg.PreparedImageCacheMaxEntries, int(cfg.PreparedImageCacheMaxBytes))
	return cache
}

func (c *preparedConversationImageCache) configure(maxEntries, maxBytes int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxEntries, c.maxBytes = maxEntries, maxBytes
	for len(c.entries) > max(maxEntries, 0) || c.totalBytes > max(maxBytes, 0) {
		oldest := c.recency.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(preparedConversationImageCacheEntry)
		c.totalBytes -= len(entry.image.data)
		delete(c.entries, entry.key)
		c.recency.Remove(oldest)
	}
}

func preparedConversationImageCacheKey(att AttachmentInput, maxDim int, mimeType string) string {
	identity := strings.TrimSpace(att.SHA256)
	if identity == "" {
		fileID := strings.TrimSpace(att.FileID)
		if fileID == "" {
			return ""
		}
		identity = fmt.Sprintf("%s:%s:%d:%d", fileID, strings.TrimSpace(att.StoragePath), att.FileSize, att.FileUpdatedAt.UnixNano())
	}
	if identity == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%d:%s:pixels=%d:static", imageutil.ProcessingVersion, identity, maxDim, resolveImageMimeType(mimeType), imageutil.DefaultMaxPixels)
}

// load shares work without letting one canceled request abort other waiters.
// No cache lock is held while reading, decoding or encoding an image.
func (c *preparedConversationImageCache) load(ctx context.Context, key string, load func(context.Context) (preparedConversationImage, error)) (preparedConversationImage, error) {
	if err := ctx.Err(); err != nil {
		return preparedConversationImage{}, err
	}
	if c == nil || key == "" {
		return load(ctx)
	}
	c.mu.Lock()
	if el, ok := c.entries[key]; ok {
		c.recency.MoveToFront(el)
		result := el.Value.(preparedConversationImageCacheEntry).image
		c.mu.Unlock()
		return result, nil
	}
	f := c.inflight[key]
	if f == nil {
		workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f = &preparedImageFlight{done: make(chan struct{}), cancel: cancel}
		c.inflight[key] = f
		go func() {
			result, err := load(workCtx)
			if err == nil && workCtx.Err() == nil {
				c.put(key, result)
			}
			c.mu.Lock()
			f.image, f.err = result, err
			if c.inflight[key] == f {
				delete(c.inflight, key)
			}
			close(f.done)
			c.mu.Unlock()
			cancel()
		}()
	}
	f.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if c.inflight[key] == f {
				delete(c.inflight, key)
			}
		}
		c.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return preparedConversationImage{}, ctx.Err()
	case <-f.done:
		return f.image, f.err
	}
}

func (c *preparedConversationImageCache) get(key string) (preparedConversationImage, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return preparedConversationImage{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return preparedConversationImage{}, false
	}
	c.recency.MoveToFront(element)
	return element.Value.(preparedConversationImageCacheEntry).image, true
}

func (c *preparedConversationImageCache) put(key string, image preparedConversationImage) {
	if c == nil || strings.TrimSpace(key) == "" || len(image.data) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxEntries <= 0 || c.maxBytes <= 0 || len(image.data) > c.maxBytes {
		return
	}

	if existing, ok := c.entries[key]; ok {
		entry := existing.Value.(preparedConversationImageCacheEntry)
		c.totalBytes -= len(entry.image.data)
		entry.image = image
		existing.Value = entry
		c.totalBytes += len(image.data)
		c.recency.MoveToFront(existing)
	} else {
		element := c.recency.PushFront(preparedConversationImageCacheEntry{key: key, image: image})
		c.entries[key] = element
		c.totalBytes += len(image.data)
	}

	for len(c.entries) > c.maxEntries || c.totalBytes > c.maxBytes {
		oldest := c.recency.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(preparedConversationImageCacheEntry)
		delete(c.entries, entry.key)
		c.totalBytes -= len(entry.image.data)
		c.recency.Remove(oldest)
	}
}
