// Package attachmentdelivery issues short lived, repeatable attachment capabilities.
package attachmentdelivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appstorage "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/objectstorage"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

const (
	DefaultTTL          = repository.AttachmentGrantMaxTTL
	MaxFileBytes  int64 = 100 << 20
	maxStageBytes int64 = 1 << 30
	maxStageFiles       = 100
	RoutePath           = "/attachments/fetch/:token"
)

var (
	ErrUnavailable = errors.New("attachment HTTPS delivery unavailable")
	ErrNotFound    = errors.New("attachment grant unavailable")
	ErrLimit       = errors.New("attachment staging limit exceeded")
)

type FileLookup interface {
	GetActiveFileObjectByID(context.Context, uint, string) (*domainconversation.FileObject, error)
}

type Link struct {
	URL       string
	ExpiresAt time.Time
	SizeBytes int64
	MIMEType  string
	FileName  string
}

type Content struct {
	Reader    io.ReadCloser
	SizeBytes int64
	MIMEType  string
	FileName  string
}

// Service holds metadata in the configured cache and bounded prepared copies in
// a private directory. Expiry is encoded on disk so lost cache keys cannot leak files.
type Service struct {
	cfg      *config.Runtime
	files    FileLookup
	grants   repository.AttachmentGrantRepository
	stores   appstorage.Provider
	readers  chan struct{}
	stageMu  sync.Mutex
	activeMu sync.Mutex
	active   map[string]map[*leasedReader]struct{}
	now      func() time.Time
}

func NewService(cfg *config.Runtime, files FileLookup, grants repository.AttachmentGrantRepository, stores appstorage.Provider) *Service {
	return &Service{cfg: cfg, files: files, grants: grants, stores: stores, readers: make(chan struct{}, 16), active: make(map[string]map[*leasedReader]struct{}), now: time.Now}
}

func (s *Service) baseURL() (string, error) {
	if s == nil || s.cfg == nil || s.grants == nil {
		return "", ErrUnavailable
	}
	raw := strings.TrimRight(strings.TrimSpace(s.cfg.Snapshot().PublicAPIBaseURL), "/")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", ErrUnavailable
	}
	return raw, nil
}

func (s *Service) Available() bool { _, err := s.baseURL(); return err == nil }

func (s *Service) source(ctx context.Context, user uint, id string) (*domainconversation.FileObject, error) {
	if s.files == nil || user == 0 || strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	f, err := s.files.GetActiveFileObjectByID(ctx, user, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrFileNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if f == nil || f.UserID != user || f.FileID != id || f.StoragePath == "" || f.SHA256 == "" || f.SizeBytes < 0 || (f.ExpiresAt != nil && !s.now().Before(*f.ExpiresAt)) {
		return nil, ErrNotFound
	}
	return f, nil
}

func bindSource(g *repository.AttachmentGrant, f *domainconversation.FileObject) {
	g.FileID = f.FileID
	g.SourceSHA256 = f.SHA256
	g.SourcePath = f.StoragePath
	g.SourceSize = f.SizeBytes
}

func (s *Service) IssueStored(ctx context.Context, user uint, fileID, runID string, ttl time.Duration) (Link, error) {
	if _, err := s.baseURL(); err != nil {
		return Link{}, err
	}
	f, err := s.source(ctx, user, fileID)
	if err != nil {
		return Link{}, err
	}
	if f.SizeBytes > MaxFileBytes {
		return Link{}, ErrLimit
	}
	g := repository.AttachmentGrant{SourceKind: repository.AttachmentSourceStored, UserID: user, RunID: runID, SHA256: f.SHA256, SizeBytes: f.SizeBytes, MIMEType: contentType(f.MimeType, f.FileName), FileName: f.FileName}
	bindSource(&g, f)
	return s.issue(ctx, g, ttl)
}

func (s *Service) StagePrepared(ctx context.Context, user uint, sourceFileID string, data []byte, mimeType, name, runID string) (Link, error) {
	if int64(len(data)) > MaxFileBytes {
		return Link{}, ErrLimit
	}
	return s.stage(ctx, user, sourceFileID, bytes.NewReader(data), mimeType, name, runID)
}

// StageTemporary imports an already authorized temporary attachment reader. The
// application caller owns session authorization; request paths are never accepted.
func (s *Service) StageTemporary(ctx context.Context, user uint, reader io.Reader, mimeType, name, runID string) (Link, error) {
	return s.stage(ctx, user, "", reader, mimeType, name, runID)
}

func (s *Service) stage(ctx context.Context, user uint, sourceID string, reader io.Reader, mimeType, name, runID string) (Link, error) {
	if _, err := s.baseURL(); err != nil {
		return Link{}, err
	}
	if user == 0 || runID == "" || reader == nil {
		return Link{}, repository.ErrInvalidInput
	}
	g := repository.AttachmentGrant{SourceKind: repository.AttachmentSourceTemporary, UserID: user, RunID: runID, MIMEType: contentType(mimeType, name), FileName: name, ExpiresAt: s.now().Add(DefaultTTL)}
	if sourceID != "" {
		f, err := s.source(ctx, user, sourceID)
		if err != nil {
			return Link{}, err
		}
		bindSource(&g, f)
		g.SourceKind = repository.AttachmentSourcePrepared
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	dir := s.stageDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Link{}, err
	}
	count, total, err := s.sweepLocked()
	if err != nil {
		return Link{}, err
	}
	if count >= maxStageFiles {
		return Link{}, ErrLimit
	}
	// A conservative reservation keeps concurrent imports within the disk budget.
	if total+MaxFileBytes > maxStageBytes {
		return Link{}, ErrLimit
	}
	random, err := randomToken()
	if err != nil {
		return Link{}, err
	}
	g.StageName = fmt.Sprintf("%d-%s", g.ExpiresAt.Unix()+1, random)
	path := filepath.Join(dir, g.StageName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Link{}, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.Remove(path)
		}
	}()
	h := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(&contextReader{ctx: ctx, reader: reader}, MaxFileBytes+1), make([]byte, 32<<10))
	closeErr := f.Close()
	if copyErr != nil {
		return Link{}, copyErr
	}
	if closeErr != nil {
		return Link{}, closeErr
	}
	if n > MaxFileBytes {
		return Link{}, ErrLimit
	}
	g.SizeBytes = n
	g.SHA256 = hex.EncodeToString(h.Sum(nil))
	link, err := s.issue(ctx, g, DefaultTTL)
	if err != nil {
		return Link{}, err
	}
	success = true
	return link, nil
}

func (s *Service) issue(ctx context.Context, g repository.AttachmentGrant, ttl time.Duration) (Link, error) {
	if err := ctx.Err(); err != nil {
		return Link{}, err
	}
	base, err := s.baseURL()
	if err != nil {
		return Link{}, err
	}
	if g.RunID == "" || g.UserID == 0 {
		return Link{}, repository.ErrInvalidInput
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > DefaultTTL {
		ttl = DefaultTTL
	}
	if g.ExpiresAt.IsZero() {
		g.ExpiresAt = s.now().Add(ttl)
	}
	token, err := randomToken()
	if err != nil {
		return Link{}, err
	}
	if err = s.grants.PutAttachmentGrant(ctx, tokenHash(token), g, ttl); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return Link{}, ErrNotFound
		}
		return Link{}, err
	}
	return Link{URL: base + "/api/v1/attachments/fetch/" + token, ExpiresAt: g.ExpiresAt, SizeBytes: g.SizeBytes, MIMEType: g.MIMEType, FileName: g.FileName}, nil
}

func (s *Service) validate(ctx context.Context, token string) (*repository.AttachmentGrant, error) {
	if len(token) != 43 || s == nil || s.grants == nil {
		return nil, ErrNotFound
	}
	g, err := s.grants.GetAttachmentGrant(ctx, tokenHash(token))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if g == nil || !s.now().Before(g.ExpiresAt) {
		return nil, ErrNotFound
	}
	if g.UserID == 0 || g.RunID == "" || g.SizeBytes < 0 || g.SizeBytes > MaxFileBytes {
		return nil, ErrNotFound
	}
	switch g.SourceKind {
	case repository.AttachmentSourceStored:
		if g.FileID == "" || g.StageName != "" {
			return nil, ErrNotFound
		}
	case repository.AttachmentSourcePrepared:
		if g.FileID == "" || g.StageName == "" {
			return nil, ErrNotFound
		}
	case repository.AttachmentSourceTemporary:
		if g.FileID != "" || g.StageName == "" {
			return nil, ErrNotFound
		}
	default:
		return nil, ErrNotFound
	}
	if g.FileID != "" {
		f, err := s.source(ctx, g.UserID, g.FileID)
		if err != nil {
			return nil, err
		}
		if f.SHA256 != g.SourceSHA256 || f.SizeBytes != g.SourceSize || f.StoragePath != g.SourcePath {
			return nil, ErrNotFound
		}
	}
	if g.StageName != "" {
		if !validStageName(g.StageName) {
			return nil, ErrNotFound
		}
		info, err := os.Lstat(filepath.Join(s.stageDir(), g.StageName))
		if err != nil || !info.Mode().IsRegular() || info.Size() != g.SizeBytes {
			return nil, ErrNotFound
		}
	}
	return g, nil
}

// Open validates every GET/HEAD. HEAD never opens the object body. GET waits for
// a bounded reader permit with cancellation, then rechecks the grant and source.
func (s *Service) Open(ctx context.Context, token string, head bool) (*Content, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g, err := s.validate(ctx, token)
	if err != nil {
		return nil, err
	}
	content := &Content{SizeBytes: g.SizeBytes, MIMEType: g.MIMEType, FileName: g.FileName}
	if head {
		return content, nil
	}
	select {
	case s.readers <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-s.readers }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	g, err = s.validate(ctx, token)
	if err != nil {
		release()
		return nil, err
	}
	readCtx, cancel := context.WithDeadline(ctx, g.ExpiresAt)
	var reader io.ReadCloser
	if g.StageName != "" {
		reader, err = os.Open(filepath.Join(s.stageDir(), g.StageName))
	} else {
		if s.stores == nil {
			err = ErrUnavailable
		} else {
			var store objectstorage.Store
			store, err = s.stores.Open(readCtx)
			if err == nil {
				var info objectstorage.ObjectInfo
				reader, info, err = store.Open(readCtx, g.SourcePath)
				if err == nil && info.SizeBytes != g.SizeBytes {
					_ = reader.Close()
					reader = nil
					err = ErrNotFound
				}
			}
		}
	}
	if errors.Is(err, objectstorage.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		err = ErrNotFound
	}
	if err != nil {
		if reader != nil {
			_ = reader.Close()
		}
		cancel()
		release()
		return nil, err
	}
	content.Reader = reader
	wrapped := &leasedReader{reader: content.Reader, ctx: readCtx, cancel: cancel, release: release, done: make(chan struct{})}
	wrapped.release = func() {
		release()
		s.activeMu.Lock()
		delete(s.active[g.RunID], wrapped)
		if len(s.active[g.RunID]) == 0 {
			delete(s.active, g.RunID)
		}
		s.activeMu.Unlock()
	}
	s.activeMu.Lock()
	if s.active[g.RunID] == nil {
		s.active[g.RunID] = make(map[*leasedReader]struct{})
	}
	s.active[g.RunID][wrapped] = struct{}{}
	s.activeMu.Unlock()
	// Registration precedes this check, so a concurrent ReleaseRun either sees
	// this active reader or its revocation is observed here.
	if _, err := s.grants.GetAttachmentGrant(ctx, tokenHash(token)); err != nil {
		_ = wrapped.Close()
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	content.Reader = wrapped
	go func() {
		select {
		case <-readCtx.Done():
			_ = wrapped.Close()
		case <-wrapped.done:
		}
	}()
	return content, nil
}

func (s *Service) ReleaseRun(ctx context.Context, runID string) error {
	grants, err := s.grants.RevokeAttachmentRun(ctx, runID)
	if err != nil {
		return err
	}
	s.activeMu.Lock()
	var readers []*leasedReader
	for reader := range s.active[runID] {
		readers = append(readers, reader)
	}
	s.activeMu.Unlock()
	for _, reader := range readers {
		_ = reader.Close()
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	for _, g := range grants {
		if validStageName(g.StageName) {
			if e := os.Remove(filepath.Join(s.stageDir(), g.StageName)); e != nil && !errors.Is(e, os.ErrNotExist) {
				err = errors.Join(err, e)
			}
		}
	}
	return err
}

func (s *Service) stageDir() string {
	return filepath.Join(s.cfg.Snapshot().StorageRootDir, "attachment-delivery")
}

// Sweep removes expired copies independently of the cache. No caller supplied
// paths are traversed; directory entries must match the generated naming scheme.
func (s *Service) Sweep() error {
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	_, _, err := s.sweepLocked()
	return err
}
func (s *Service) sweepLocked() (int, int64, error) {
	entries, err := os.ReadDir(s.stageDir())
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var total int64
	for _, e := range entries {
		if !validStageName(e.Name()) {
			continue
		}
		parts := strings.SplitN(e.Name(), "-", 2)
		expiry, _ := strconv.ParseInt(parts[0], 10, 64)
		path := filepath.Join(s.stageDir(), e.Name())
		if !s.now().Before(time.Unix(expiry, 0)) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return count, total, err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return count, total, err
		}
		if info.Mode().IsRegular() {
			count++
			total += info.Size()
		}
	}
	return count, total, nil
}
func (s *Service) StartBackgroundWorkers(ctx context.Context) {
	_ = s.Sweep()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.Sweep()
			}
		}
	}()
}

func randomToken() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:]), err
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func validStageName(name string) bool {
	if filepath.Base(name) != name {
		return false
	}
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		return false
	}
	if expiry, err := strconv.ParseInt(parts[0], 10, 64); err != nil || expiry <= 0 {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	return err == nil && len(data) == 32 && base64.RawURLEncoding.EncodeToString(data) == parts[1]
}
func contentType(value, name string) string {
	if typ, params, err := mime.ParseMediaType(value); err == nil {
		return mime.FormatMediaType(typ, params)
	}
	if typ := mime.TypeByExtension(filepath.Ext(name)); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type leasedReader struct {
	reader  io.ReadCloser
	ctx     context.Context
	cancel  context.CancelFunc
	release func()
	once    sync.Once
	done    chan struct{}
}

func (r *leasedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func (r *leasedReader) Close() error {
	var err error
	r.once.Do(func() { r.cancel(); err = r.reader.Close(); r.release(); close(r.done) })
	return err
}
