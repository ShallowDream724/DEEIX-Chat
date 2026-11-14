package speech

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
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
	metadata      map[string]cacheMetadata
	audio         map[string]*cachedAudio
	handles       map[*storedAudio]struct{}
	gcStop        chan struct{}
	gcDone        chan struct{}
	closed        bool
	closeOnce     sync.Once
	closeErr      error
}

// Keep only eviction metadata in memory; text, billing records and audio stay on
// disk. Capacity checks never need to reread every JSON file for an audio chunk.
type cacheMetadata struct {
	operation, status, reference string
	createdAt                    time.Time
}

type cachedAudio struct {
	size          int64
	lastAccess    time.Time
	readers       int
	writers       int
	deletePending bool
}

func (a *cachedAudio) pinned() bool { return a.readers > 0 || a.writers > 0 }

var safeID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`)

const (
	maxSpeechRecords = 10000
	speechGCInterval = time.Minute
)

func NewFileStore(dir string, maxBytes int64, ttl time.Duration) (*FileStore, error) {
	return newFileStore(dir, maxBytes, ttl, speechGCInterval)
}

func newFileStore(dir string, maxBytes int64, ttl, gcInterval time.Duration) (*FileStore, error) {
	if maxBytes <= 0 {
		maxBytes = 256 << 20
	}
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	if gcInterval <= 0 {
		gcInterval = speechGCInterval
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	lock, e := lockStore(filepath.Join(dir, ".owner.lock"))
	if e != nil {
		return nil, e
	}
	s := &FileStore{
		dir: dir, maxBytes: maxBytes, ttl: ttl, lock: lock,
		metadata: map[string]cacheMetadata{}, audio: map[string]*cachedAudio{}, handles: map[*storedAudio]struct{}{},
		gcStop: make(chan struct{}), gcDone: make(chan struct{}),
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		lock.Close()
		return nil, e
	}
	for _, v := range entries {
		if !v.Type().IsRegular() {
			continue
		}
		if filepath.Ext(v.Name()) == ".audio" {
			i, e := v.Info()
			if e == nil {
				s.used += i.Size()
				id := strings.TrimSuffix(v.Name(), ".audio")
				if safeID.MatchString(id) {
					s.audio[id] = &cachedAudio{size: i.Size(), lastAccess: i.ModTime()}
				}
			}
		}
		if filepath.Ext(v.Name()) == ".json" {
			s.records++
			b, e := os.ReadFile(filepath.Join(dir, v.Name()))
			if e != nil {
				continue
			}
			var r domain.Record
			if json.Unmarshal(b, &r) != nil || r.ID != strings.TrimSuffix(v.Name(), ".json") || !safeID.MatchString(r.ID) {
				continue
			}
			s.indexRecord(r)
			if r.Status == "pending" {
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
	_ = s.ensureCapacityLocked(0)
	s.mu.Unlock()
	go s.collect(gcInterval)
	return s, nil
}

func (s *FileStore) collect(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(s.gcDone)
	for {
		select {
		case <-s.gcStop:
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				s.cleanupLocked()
				_ = s.ensureCapacityLocked(0)
			}
			s.mu.Unlock()
		}
	}
}

func (s *FileStore) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.gcStop)
		handles := make([]*storedAudio, 0, len(s.handles))
		for handle := range s.handles {
			handles = append(handles, handle)
		}
		s.mu.Unlock()
		<-s.gcDone
		// Closing OS handles can interrupt a reader. Do not hold the store mutex
		// while waiting for those handles to finish and release their pins.
		for _, handle := range handles {
			_ = handle.Close()
		}
		s.closeErr = s.lock.Close()
	})
	return s.closeErr
}

func (s *FileStore) Configure(maxBytes int64, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	changed := false
	if maxBytes > 0 {
		changed = maxBytes != s.maxBytes
		s.maxBytes = maxBytes
	}
	if ttl > 0 && ttl != s.ttl {
		s.ttl = ttl
		changed = true
	}
	if changed {
		s.cleanupLocked()
		_ = s.ensureCapacityLocked(0)
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
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if s.closed {
		return domain.ErrUnavailable
	}
	_, statErr := os.Stat(p)
	fresh := os.IsNotExist(statErr)
	if fresh && s.records >= maxSpeechRecords {
		if time.Since(s.lastGC) > speechGCInterval {
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
	s.indexRecord(r)
	return nil
}

func (s *FileStore) indexRecord(r domain.Record) {
	s.metadata[r.ID] = cacheMetadata{operation: r.Operation, status: r.Status, reference: r.ReferenceID, createdAt: r.CreatedAt}
}

func (s *FileStore) discardAudio(id string, now time.Time) bool {
	r, exists := s.metadata[id]
	return exists && r.operation == "tts" && (r.status == "uncertain" || r.status == "cancelled" || (r.status == "complete" && now.Sub(r.createdAt) > s.ttl))
}

func (s *FileStore) cleanupLocked() {
	now := time.Now()
	for id, audio := range s.audio {
		if audio.deletePending || s.discardAudio(id, now) {
			_ = s.removeAudioLocked(id)
		}
	}
	for id, r := range s.metadata {
		// Preserve the record while audio remains, including a custom retention
		// longer than the metadata minimum or a reader still holding the file.
		if s.audio[id] != nil || (s.audio[r.reference] != nil && s.audio[r.reference].pinned()) {
			continue
		}
		if now.Sub(r.createdAt) > 30*24*time.Hour && (r.reference != "" || r.status == "complete" || r.status == "cancelled") {
			p, _ := s.path(id, ".json")
			if err := os.Remove(p); err == nil || errors.Is(err, os.ErrNotExist) {
				s.records--
				delete(s.metadata, id)
			}
		}
	}
	s.lastGC = now
}

func (s *FileStore) ensureCapacityLocked(additional int64) error {
	if additional < 0 || additional > s.maxBytes {
		return domain.ErrLimited
	}
	if s.used <= s.maxBytes-additional {
		return nil
	}
	s.cleanupLocked()
	if s.used <= s.maxBytes-additional {
		return nil
	}
	var candidates []string
	for id, audio := range s.audio {
		r := s.metadata[id]
		if !audio.pinned() && r.operation == "tts" && r.status == "complete" {
			candidates = append(candidates, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := s.audio[candidates[i]], s.audio[candidates[j]]
		if a.lastAccess.Equal(b.lastAccess) {
			return candidates[i] < candidates[j]
		}
		return a.lastAccess.Before(b.lastAccess)
	})
	for _, id := range candidates {
		_ = s.removeAudioLocked(id)
		if s.used <= s.maxBytes-additional {
			return nil
		}
	}
	return domain.ErrLimited
}

func (s *FileStore) CreateAudio(ctx context.Context, id string) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, e := s.path(id, ".audio")
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, domain.ErrUnavailable
	}
	if time.Since(s.lastGC) > speechGCInterval {
		s.cleanupLocked()
	}
	if e = s.ensureCapacityLocked(1); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	s.audio[id] = &cachedAudio{lastAccess: time.Now(), writers: 1}
	handle := &storedAudio{file: f, store: s, id: id, writer: true}
	s.handles[handle] = struct{}{}
	return handle, nil
}

type storedAudio struct {
	file      *os.File
	store     *FileStore
	id        string
	writer    bool
	closeOnce sync.Once
	closeErr  error
}

func (f *storedAudio) Read(p []byte) (int, error) { return f.file.Read(p) }

func (w *storedAudio) Write(b []byte) (int, error) {
	s := w.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, domain.ErrUnavailable
	}
	if _, open := s.handles[w]; !open || !w.writer {
		return 0, os.ErrClosed
	}
	if err := s.ensureCapacityLocked(int64(len(b))); err != nil {
		return 0, err
	}
	n, e := w.file.Write(b)
	s.used += int64(n)
	s.audio[w.id].size += int64(n)
	return n, e
}

func (f *storedAudio) Close() error {
	f.closeOnce.Do(func() {
		f.closeErr = f.file.Close()
		s := f.store
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.handles, f)
		if audio := s.audio[f.id]; audio != nil {
			if f.writer {
				audio.writers--
			} else {
				audio.readers--
			}
			if !audio.pinned() && (audio.deletePending || s.discardAudio(f.id, time.Now())) {
				_ = s.removeAudioLocked(f.id)
			}
		}
		if !s.closed {
			_ = s.ensureCapacityLocked(0)
		}
	})
	return f.closeErr
}

func (s *FileStore) OpenAudio(ctx context.Context, id string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, e := s.path(id, ".audio")
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, domain.ErrUnavailable
	}
	if r := s.metadata[id]; r.operation == "tts" && r.status == "complete" && time.Since(r.createdAt) > s.ttl {
		_ = s.removeAudioLocked(id)
		return nil, domain.ErrExpired
	}
	if audio := s.audio[id]; audio != nil && audio.deletePending {
		return nil, domain.ErrNotFound
	}
	f, e := os.Open(p)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) && s.metadata[id].status == "complete" && s.metadata[id].operation == "tts" {
			return nil, domain.ErrExpired
		}
		return nil, e
	}
	audio := s.audio[id]
	if audio == nil {
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, domain.ErrInvalid
		}
		audio = &cachedAudio{size: info.Size()}
		s.audio[id] = audio
		s.used += info.Size()
	}
	audio.readers++
	audio.lastAccess = time.Now()
	handle := &storedAudio{file: f, store: s, id: id}
	s.handles[handle] = struct{}{}
	return handle, nil
}
func (s *FileStore) DeleteAudio(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return domain.ErrUnavailable
	}
	return s.removeAudioLocked(id)
}

func (s *FileStore) removeAudioLocked(id string) error {
	p, e := s.path(id, ".audio")
	if e != nil {
		return e
	}
	audio := s.audio[id]
	if audio != nil && audio.pinned() {
		audio.deletePending = true
		return nil
	}
	e = os.Remove(p)
	if e == nil || errors.Is(e, os.ErrNotExist) {
		if audio != nil {
			s.used -= audio.size
			delete(s.audio, id)
		}
		return nil
	}
	return e
}
