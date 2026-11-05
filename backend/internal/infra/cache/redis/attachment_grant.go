package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	goredis "github.com/go-redis/redis/v8"
)

const putAttachmentGrantScript = `
if redis.call('EXISTS', KEYS[3]) == 1 then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SADD', KEYS[2], KEYS[1])
local ttl = redis.call('PTTL', KEYS[2])
if ttl < tonumber(ARGV[2]) then redis.call('PEXPIRE', KEYS[2], ARGV[2]) end
return 1
`

const revokeAttachmentRunScript = `
redis.call('SET', KEYS[2], '1', 'PX', ARGV[1])
local keys = redis.call('SMEMBERS', KEYS[1])
local result = {}
for _, key in ipairs(keys) do
  local value = redis.call('GET', key)
  if value then table.insert(result, value) end
  redis.call('DEL', key)
end
redis.call('DEL', KEYS[1])
return result
`

type attachmentGrants struct{ client *goredis.Client }

func NewAttachmentGrants(c *goredis.Client) repository.AttachmentGrantRepository {
	return &attachmentGrants{client: c}
}
func attachmentRunKey(run string) string {
	digest := sha256.Sum256([]byte(run))
	return "attachment:run:" + hex.EncodeToString(digest[:])
}
func (s *attachmentGrants) PutAttachmentGrant(ctx context.Context, hash string, g repository.AttachmentGrant, ttl time.Duration) error {
	if hash == "" || g.RunID == "" || ttl <= 0 {
		return repository.ErrInvalidInput
	}
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	// Retain the index for the longest grant; issuing a shorter grant must not shorten it.
	result, err := s.client.Eval(ctx, putAttachmentGrantScript, []string{"attachment:grant:" + hash, attachmentRunKey(g.RunID), attachmentRunKey(g.RunID) + ":revoked"}, data, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return repository.ErrNotFound
	}
	return nil
}
func (s *attachmentGrants) GetAttachmentGrant(ctx context.Context, hash string) (*repository.AttachmentGrant, error) {
	data, err := s.client.Get(ctx, "attachment:grant:"+hash).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var g repository.AttachmentGrant
	if err = json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	if !time.Now().Before(g.ExpiresAt) {
		return nil, repository.ErrNotFound
	}
	return &g, nil
}
func (s *attachmentGrants) RevokeAttachmentRun(ctx context.Context, run string) ([]repository.AttachmentGrant, error) {
	values, err := s.client.Eval(ctx, revokeAttachmentRunScript, []string{attachmentRunKey(run), attachmentRunKey(run) + ":revoked"}, repository.AttachmentGrantMaxTTL.Milliseconds()).StringSlice()
	if err != nil {
		return nil, err
	}
	var result []repository.AttachmentGrant
	for _, value := range values {
		var g repository.AttachmentGrant
		if err = json.Unmarshal([]byte(value), &g); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, nil
}
