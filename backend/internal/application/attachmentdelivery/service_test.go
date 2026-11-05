package attachmentdelivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/cache/memory"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type lookup struct {
	file *domainconversation.FileObject
}

func (l *lookup) GetActiveFileObjectByID(_ context.Context, user uint, id string) (*domainconversation.FileObject, error) {
	if l.file == nil || l.file.UserID != user || l.file.FileID != id {
		return nil, repository.ErrNotFound
	}
	f := *l.file
	return &f, nil
}

type provider struct{ store *testStore }

func (p provider) Open(context.Context) (objectstorage.Store, error) { return p.store, nil }

type testStore struct {
	size    int64
	opens   atomic.Int32
	reads   atomic.Int32
	maxRead atomic.Int32
	closed  atomic.Int32
}

func (s *testStore) Open(context.Context, string) (io.ReadCloser, objectstorage.ObjectInfo, error) {
	s.opens.Add(1)
	return &sizedReader{left: s.size, store: s}, objectstorage.ObjectInfo{SizeBytes: s.size}, nil
}
func (*testStore) Put(context.Context, string, io.Reader, objectstorage.PutOptions) (objectstorage.ObjectInfo, error) {
	panic("unused")
}
func (*testStore) Delete(context.Context, string) error { panic("unused") }
func (*testStore) Materialize(context.Context, string) (string, func(), error) {
	panic("delivery must not materialize")
}

type sizedReader struct {
	left  int64
	store *testStore
}

func (r *sizedReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	r.store.reads.Add(1)
	if int32(len(p)) > r.store.maxRead.Load() {
		r.store.maxRead.Store(int32(len(p)))
	}
	n := int64(len(p))
	if n > r.left {
		n = r.left
	}
	clear(p[:n])
	r.left -= n
	return int(n), nil
}
func (r *sizedReader) Close() error { r.store.closed.Add(1); return nil }
func fixture(t *testing.T, size int64) (*Service, *lookup, *testStore) {
	t.Helper()
	files := &lookup{file: &domainconversation.FileObject{UserID: 7, FileID: "file-1", StoragePath: "objects/original", SHA256: "trusted-sha", SizeBytes: size, MimeType: "application/pdf", FileName: "report.pdf"}}
	store := &testStore{size: size}
	cfg := config.NewRuntime(config.Config{PublicAPIBaseURL: "https://api.example.test", StorageRootDir: t.TempDir()})
	return NewService(cfg, files, memory.NewAttachmentGrants(memory.New()), provider{store}), files, store
}
func token(t *testing.T, l Link) string {
	t.Helper()
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(u.Path)
}

func TestStoredRepeatedHEADAndGETStreamsBounded(t *testing.T) {
	s, _, store := fixture(t, MaxFileBytes)
	ctx := context.Background()
	link, err := s.IssueStored(ctx, 7, "file-1", "run-1", DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link.URL, "https://api.example.test/api/v1/attachments/fetch/") {
		t.Fatal(link.URL)
	}
	if store.opens.Load() != 0 {
		t.Fatal("issuance opened body")
	}
	for range 2 {
		head, err := s.Open(ctx, token(t, link), true)
		if err != nil || head.Reader != nil || head.SizeBytes != MaxFileBytes {
			t.Fatalf("HEAD %+v %v", head, err)
		}
	}
	if store.opens.Load() != 0 {
		t.Fatal("HEAD opened object")
	}
	for range 2 {
		content, err := s.Open(ctx, token(t, link), false)
		if err != nil {
			t.Fatal(err)
		}
		if store.reads.Load() != 0 && store.opens.Load() == 1 {
			t.Fatal("Open consumed body")
		}
		n, err := io.CopyBuffer(io.Discard, content.Reader, make([]byte, 32<<10))
		_ = content.Reader.Close()
		if err != nil || n != MaxFileBytes {
			t.Fatalf("stream %d %v", n, err)
		}
	}
	if store.maxRead.Load() > 32<<10 {
		t.Fatalf("unbounded read %d", store.maxRead.Load())
	}
	if len(s.readers) != 0 || store.closed.Load() != 2 {
		t.Fatal("reader permit leak")
	}
}

func TestStoredRevokesOnSourceChanges(t *testing.T) {
	for _, change := range []string{"delete", "owner", "sha", "size", "path", "expiry"} {
		t.Run(change, func(t *testing.T) {
			s, files, _ := fixture(t, 12)
			link, err := s.IssueStored(context.Background(), 7, "file-1", "run", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "delete":
				files.file = nil
			case "owner":
				files.file.UserID = 8
			case "sha":
				files.file.SHA256 = "different"
			case "size":
				files.file.SizeBytes++
			case "path":
				files.file.StoragePath = "other"
			case "expiry":
				expired := time.Now().Add(-time.Minute)
				files.file.ExpiresAt = &expired
			}
			for _, head := range []bool{true, false} {
				if _, err = s.Open(context.Background(), token(t, link), head); !errors.Is(err, ErrNotFound) {
					t.Fatalf("access survived %s: %v", change, err)
				}
			}
		})
	}
}

func TestExpiryAndReleaseRun(t *testing.T) {
	s, _, _ := fixture(t, 12)
	link, err := s.IssueStored(context.Background(), 7, "file-1", "run", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err = s.Open(context.Background(), token(t, link), true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	s.now = time.Now
	link, err = s.IssueStored(context.Background(), 7, "file-1", "run", DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseRun(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Open(context.Background(), token(t, link), true); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCanceledReaderAndWaitingRequestReleasePermit(t *testing.T) {
	s, _, store := fixture(t, 12)
	s.readers = make(chan struct{}, 1)
	link, err := s.IssueStored(context.Background(), 7, "file-1", "run", DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	content, err := s.Open(ctx, token(t, link), false)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	if _, err = s.Open(waitCtx, token(t, link), false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-content.Reader.(*leasedReader).done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not close")
	}
	if len(s.readers) != 0 || store.closed.Load() != 1 {
		t.Fatal("permit leak")
	}
	if _, err = content.Reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPreparedCopiesStayBoundToSourceAndTemporaryCleanup(t *testing.T) {
	s, files, _ := fixture(t, 12)
	ctx := context.Background()
	link, err := s.StagePrepared(ctx, 7, "file-1", []byte("small image"), "image/png", "out.png", "run")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		content, err := s.Open(ctx, token(t, link), false)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(content.Reader)
		_ = content.Reader.Close()
		if string(got) != "small image" {
			t.Fatal(string(got))
		}
	}
	files.file = nil
	if _, err = s.Open(ctx, token(t, link), true); !errors.Is(err, ErrNotFound) {
		t.Fatal("prepared copy bypassed deletion", err)
	}
	if err = s.ReleaseRun(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.stageDir())
	if len(entries) != 0 {
		t.Fatal("release left stage")
	}
	link, err = s.StageTemporary(ctx, 7, bytes.NewReader([]byte("temporary")), "text/plain", "tmp.txt", "temp-run")
	if err != nil {
		t.Fatal(err)
	}
	if link.SizeBytes != 9 {
		t.Fatal(link)
	}
	// Simulate a Redis TTL loss and process restart. Disk expiry must stand alone.
	restarted := NewService(s.cfg, nil, memory.NewAttachmentGrants(memory.New()), nil)
	restarted.now = func() time.Time { return time.Now().Add(DefaultTTL + time.Minute) }
	if err = restarted.Sweep(); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(s.stageDir())
	if len(entries) != 0 {
		t.Fatal("orphan stage survived expiry")
	}
}

func TestStageRejectsLimitsAndCanceledImportWithoutOrphans(t *testing.T) {
	s, _, _ := fixture(t, 12)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.StageTemporary(ctx, 7, strings.NewReader("x"), "text/plain", "x", "run"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.stageDir())
	if len(entries) != 0 {
		t.Fatal("canceled stage leaked")
	}
	if err := os.MkdirAll(s.stageDir(), 0700); err != nil {
		t.Fatal(err)
	}
	for range maxStageFiles {
		random, err := randomToken()
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("%d-%s", time.Now().Add(time.Hour).Unix(), random)
		if err := os.WriteFile(filepath.Join(s.stageDir(), name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.StageTemporary(context.Background(), 7, strings.NewReader("x"), "text/plain", "x", "run"); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}

func TestBearerStoredOnlyAsDigest(t *testing.T) {
	s, _, _ := fixture(t, 1)
	link, err := s.IssueStored(context.Background(), 7, "file-1", "run", DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	raw := token(t, link)
	if _, err = s.grants.GetAttachmentGrant(context.Background(), raw); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("raw token was stored", err)
	}
	if _, err = s.grants.GetAttachmentGrant(context.Background(), tokenHash(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseRunClosesActiveStageReader(t *testing.T) {
	s, _, _ := fixture(t, 1)
	link, err := s.StageTemporary(context.Background(), 7, strings.NewReader("temporary"), "text/plain", "tmp.txt", "run")
	if err != nil {
		t.Fatal(err)
	}
	content, err := s.Open(context.Background(), token(t, link), false)
	if err != nil {
		t.Fatal(err)
	}
	reader := content.Reader.(*leasedReader)
	if err := s.ReleaseRun(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reader.done:
	default:
		t.Fatal("release left active reader")
	}
	if len(s.readers) != 0 {
		t.Fatal("release leaked permit")
	}
	entries, err := os.ReadDir(s.stageDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("stage remains: %v %v", entries, err)
	}
}

func TestOversizedTemporaryImportIsBoundedAndRemoved(t *testing.T) {
	s, _, store := fixture(t, MaxFileBytes+1000)
	_, err := s.StageTemporary(context.Background(), 7, &sizedReader{left: MaxFileBytes + 1000, store: store}, "application/octet-stream", "large.bin", "run")
	if !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if store.maxRead.Load() > 32<<10 {
		t.Fatalf("unbounded staging read %d", store.maxRead.Load())
	}
	entries, err := os.ReadDir(s.stageDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("oversized stage remains: %v %v", entries, err)
	}
}

type gatedReader struct {
	entered, resume chan struct{}
	sent            bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	close(r.entered)
	<-r.resume
	r.sent = true
	return copy(p, "x"), nil
}

func TestRunRevocationCannotBeUndoneByInflightStage(t *testing.T) {
	s, _, _ := fixture(t, 1)
	reader := &gatedReader{entered: make(chan struct{}), resume: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := s.StageTemporary(context.Background(), 7, reader, "text/plain", "tmp.txt", "run")
		result <- err
	}()
	<-reader.entered
	// Revoke the run atomically while its first capability is still being prepared.
	if _, err := s.grants.RevokeAttachmentRun(context.Background(), "run"); err != nil {
		t.Fatal(err)
	}
	close(reader.resume)
	if err := <-result; !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked run issued a capability: %v", err)
	}
	entries, err := os.ReadDir(s.stageDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("inflight stage leaked: %v %v", entries, err)
	}
	if _, err := s.IssueStored(context.Background(), 7, "file-1", "run", DefaultTTL); !errors.Is(err, ErrNotFound) {
		t.Fatalf("run was resurrected: %v", err)
	}
}
