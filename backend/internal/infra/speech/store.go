package speech

import (
	"context"
	"encoding/json"
	"errors"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type FileStore struct {
	identityLocks [64]sync.Mutex
	dir           string
	maxBytes      int64
	ttl           time.Duration
	mu            sync.Mutex
	used          int64
	records       int
	lastGC        time.Time
	lock          *os.File
}

var safeID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

const maxSpeechRecords = 10000

func NewFileStore(dir string, maxBytes int64, ttl time.Duration) (*FileStore, error) {
	if maxBytes <= 0 {
		maxBytes = 512 << 20
	}
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, e := lockStore(filepath.Join(dir, ".owner.lock"))
	if e != nil {
		return nil, e
	}
	s := &FileStore{dir: dir, maxBytes: maxBytes, ttl: ttl, lock: lock}
	entries, e := os.ReadDir(dir)
	if e != nil {
		lock.Close()
		return nil, e
	}
	for _, v := range entries {
		if filepath.Ext(v.Name()) == ".audio" {
			i, e := v.Info()
			if e == nil {
				s.used += i.Size()
			}
		}
		if filepath.Ext(v.Name()) == ".json" {
			s.records++
			b, e := os.ReadFile(filepath.Join(dir, v.Name()))
			if e != nil {
				continue
			}
			var r domain.Record
			if json.Unmarshal(b, &r) == nil && r.Status == "pending" {
				r.Status = "uncertain"
				r.Error = "process_restarted"
				if e = s.Put(context.Background(), r); e != nil {
					lock.Close()
					return nil, e
				}
			}
		}
	}
	s.mu.Lock()
	s.cleanupLocked()
	s.mu.Unlock()
	return s, nil
}
func (s *FileStore) Close() error { return s.lock.Close() }
func (s *FileStore) Configure(maxBytes int64, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBytes > 0 {
		s.maxBytes = maxBytes
	}
	if ttl > 0 && ttl != s.ttl {
		s.ttl = ttl
		s.lastGC = time.Time{}
	}
}
func (s *FileStore) path(id, ext string) (string, error) {
	if !safeID.MatchString(id) {
		return "", domain.ErrInvalid
	}
	return filepath.Join(s.dir, id+ext), nil
}
func (s *FileStore) Get(ctx context.Context, id string) (domain.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(ctx, id, 0)
}
func (s *FileStore) get(ctx context.Context, id string, depth int) (domain.Record, error) {
	if depth > 1 {
		return domain.Record{}, domain.ErrInvalid
	}
	p, e := s.path(id, ".json")
	if e != nil {
		return domain.Record{}, e
	}
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		return domain.Record{}, domain.ErrNotFound
	}
	if e != nil {
		return domain.Record{}, e
	}
	var r domain.Record
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.ReferenceID != "" {
		return s.get(ctx, r.ReferenceID, depth+1)
	}
	if r.Status == "complete" && r.Operation == "tts" {
		audio, _ := s.path(r.ID, ".audio")
		_, e = os.Stat(audio)
		if time.Since(r.CreatedAt) > s.ttl || errors.Is(e, os.ErrNotExist) {
			r.Status = "expired"
		}
	}
	return r, nil
}
func (s *FileStore) Put(ctx context.Context, r domain.Record) error {
	p, e := s.path(r.ID, ".json")
	if e != nil {
		return e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, statErr := os.Stat(p)
	fresh := os.IsNotExist(statErr)
	if fresh && s.records >= maxSpeechRecords {
		if time.Since(s.lastGC) > 5*time.Minute {
			s.cleanupLocked()
		}
		if s.records >= maxSpeechRecords {
			return domain.ErrLimited
		}
	}
	f, e := os.CreateTemp(s.dir, "record-*.tmp")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(tmp, p); e != nil {
		return e
	}
	if fresh {
		s.records++
	}
	return nil
}
func (s *FileStore) cleanupLocked() {
	entries, _ := os.ReadDir(s.dir)
	for _, v := range entries {
		if filepath.Ext(v.Name()) != ".json" {
			continue
		}
		p := filepath.Join(s.dir, v.Name())
		b, e := os.ReadFile(p)
		if e != nil {
			continue
		}
		var r domain.Record
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		age := time.Since(r.CreatedAt)
		if r.Operation == "tts" && ((r.Status == "complete" && age > s.ttl) || r.Status == "uncertain" || r.Status == "cancelled") {
			audio, _ := s.path(r.ID, ".audio")
			if i, e := os.Stat(audio); e == nil {
				if os.Remove(audio) == nil {
					s.used -= i.Size()
				}
			}
		}
		if age > 30*24*time.Hour && (r.ReferenceID != "" || r.Status == "complete" || r.Status == "cancelled") {
			if os.Remove(p) == nil {
				s.records--
			}
		}
	}
	s.lastGC = time.Now()
}
func (s *FileStore) CreateAudio(ctx context.Context, id string) (io.WriteCloser, error) {
	p, e := s.path(id, ".audio")
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastGC) > 5*time.Minute {
		s.cleanupLocked()
	}
	if s.used >= s.maxBytes {
		return nil, domain.ErrLimited
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	return &limitedAudio{File: f, store: s}, nil
}

type limitedAudio struct {
	*os.File
	store *FileStore
}

func (w *limitedAudio) Write(b []byte) (int, error) {
	s := w.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used+int64(len(b)) > s.maxBytes {
		return 0, domain.ErrLimited
	}
	n, e := w.File.Write(b)
	s.used += int64(n)
	return n, e
}
func (s *FileStore) OpenAudio(ctx context.Context, id string) (io.ReadCloser, error) {
	p, e := s.path(id, ".audio")
	if e != nil {
		return nil, e
	}
	return os.Open(p)
}
func (s *FileStore) DeleteAudio(ctx context.Context, id string) error {
	p, e := s.path(id, ".audio")
	if e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, e := os.Stat(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	e = os.Remove(p)
	if e == nil {
		s.used -= info.Size()
	}
	return e
}
