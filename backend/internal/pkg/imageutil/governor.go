package imageutil

import (
	"context"
	"errors"
	"sync"
)

// DecodeGovernor limits concurrent legacy decodes by user, task count and
// estimated memory. Waiters compete when resources change; a blocked user's
// queue cannot head-of-line block another user with available capacity.
type DecodeGovernor struct {
	mu                         sync.Mutex
	changed                    chan struct{}
	users                      map[uint]int
	active, maxActive, perUser int
	bytes, maxBytes            int64
}

func NewDecodeGovernor(maxActive, perUser int, maxBytes int64) *DecodeGovernor {
	return &DecodeGovernor{changed: make(chan struct{}), users: make(map[uint]int), maxActive: maxActive, perUser: perUser, maxBytes: maxBytes}
}

var defaultDecodeGovernor = NewDecodeGovernor(2, 1, 256*1024*1024)
var ErrImageDecodeBudget = errors.New("image decode exceeds resource budget; optimize the image before upload")

func (g *DecodeGovernor) Acquire(ctx context.Context, userID uint, bytes int64) (func(), error) {
	if bytes <= 0 || bytes > g.maxBytes || g.maxActive <= 0 || g.perUser <= 0 {
		return nil, ErrImageDecodeBudget
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		if g.active < g.maxActive && g.users[userID] < g.perUser && bytes <= g.maxBytes-g.bytes {
			g.active++
			g.users[userID]++
			g.bytes += bytes
			g.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					g.mu.Lock()
					g.active--
					g.users[userID]--
					if g.users[userID] == 0 {
						delete(g.users, userID)
					}
					g.bytes -= bytes
					close(g.changed)
					g.changed = make(chan struct{})
					g.mu.Unlock()
				})
			}, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
