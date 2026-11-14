package speech

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

func cacheStore(t *testing.T, maximum int64, ttl time.Duration) *FileStore {
	t.Helper()
	store, err := newFileStore(t.TempDir(), maximum, ttl, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func completeCachedAudio(t *testing.T, store *FileStore, id, content string, created time.Time) {
	t.Helper()
	r := domain.Record{ID: id, Operation: "tts", Status: "pending", CreatedAt: created, Fingerprint: "cache-" + id}
	if err := store.Put(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	writer, err := store.CreateAudio(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(writer, strings.NewReader(content)); err != nil {
		writer.Close()
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r.Status = "complete"
	if err = store.Put(t.Context(), r); err != nil {
		t.Fatal(err)
	}
}

func TestFileStorePressureEvictsLeastRecentlyUsedCompletedAudioAndKeepsIdentity(t *testing.T) {
	store := cacheStore(t, 12, time.Hour)
	completeCachedAudio(t, store, "used", "keep", time.Now())
	completeCachedAudio(t, store, "old", "drop", time.Now())
	if err := store.Put(t.Context(), domain.Record{ID: "request-old", ReferenceID: "old", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// A replay updates recency; creation order must not override actual use.
	reader, err := store.OpenAudio(t.Context(), "used")
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	completeCachedAudio(t, store, "new", "latest", time.Now())
	retained, err := store.Get(t.Context(), "used")
	if err != nil || retained.Status != "complete" {
		t.Fatal("recently played audio was evicted", retained, err)
	}
	evicted, err := store.Get(t.Context(), "request-old")
	if err != nil || evicted.Status != "expired" || evicted.ID != "old" || evicted.Fingerprint != "cache-old" {
		t.Fatal("audio eviction lost the paid request identity", evicted, err)
	}
	if _, err = os.Stat(filepath.Join(store.dir, "old.audio")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old audio was not physically removed", err)
	}
	if _, err = os.Stat(filepath.Join(store.dir, "old.json")); err != nil {
		t.Fatal("audio pressure deleted settlement metadata", err)
	}
	if _, err = store.OpenAudio(t.Context(), "old"); !errors.Is(err, domain.ErrExpired) {
		t.Fatal("an eviction between status lookup and audio open must remain retryable as expiry", err)
	}
	store.mu.Lock()
	used := store.used
	store.mu.Unlock()
	if used != 10 {
		t.Fatal("eviction did not release exactly the removed bytes", used)
	}
}

func TestFileStoreFullQuotaProtectsReadersAndWritersUntilClose(t *testing.T) {
	for _, kind := range []string{"reader", "writer"} {
		t.Run(kind, func(t *testing.T) {
			store := cacheStore(t, 4, time.Hour)
			var pinned io.Closer
			if kind == "reader" {
				completeCachedAudio(t, store, "active", "keep", time.Now())
				reader, err := store.OpenAudio(t.Context(), "active")
				if err != nil {
					t.Fatal(err)
				}
				pinned = reader
			} else {
				writer, err := store.CreateAudio(t.Context(), "active")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = writer.Write([]byte("keep")); err != nil {
					t.Fatal(err)
				}
				// Even if completion metadata becomes visible before Close, an
				// open writer must still protect its file from pressure eviction.
				if err = store.Put(t.Context(), domain.Record{ID: "active", Operation: "tts", Status: "complete", CreatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
				pinned = writer
			}
			if _, err := store.CreateAudio(t.Context(), "new"); !errors.Is(err, domain.ErrLimited) {
				t.Fatal("quota pressure discarded active audio", err)
			}
			if _, err := os.Stat(filepath.Join(store.dir, "active.audio")); err != nil {
				t.Fatal("pinned audio disappeared", err)
			}
			if reader, ok := pinned.(io.Reader); kind == "reader" && ok {
				body, err := io.ReadAll(reader)
				if err != nil || string(body) != "keep" {
					t.Fatal("pressure interrupted playback", string(body), err)
				}
			}
			if err := pinned.Close(); err != nil {
				t.Fatal(err)
			}
			if err := pinned.Close(); err != nil {
				t.Fatal("handle close was not idempotent", err)
			}
			writer, err := store.CreateAudio(t.Context(), "new")
			if err != nil {
				t.Fatal("released audio still blocked a new synthesis", err)
			}
			writer.Close()
			record, err := store.Get(t.Context(), "active")
			if err != nil || record.Status != "expired" {
				t.Fatal("evicted completed audio became uncertain", record, err)
			}
		})
	}
}

func TestFileStorePressureSkipsPinnedAudioAndEvictsAnotherCompletedFile(t *testing.T) {
	store := cacheStore(t, 8, time.Hour)
	completeCachedAudio(t, store, "playing", "keep", time.Now())
	reader, err := store.OpenAudio(t.Context(), "playing")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	completeCachedAudio(t, store, "idle", "drop", time.Now())
	completeCachedAudio(t, store, "new", "next", time.Now())
	if record, err := store.Get(t.Context(), "idle"); err != nil || record.Status != "expired" {
		t.Fatal("pressure did not choose idle audio", record, err)
	}
	if body, err := io.ReadAll(reader); err != nil || string(body) != "keep" {
		t.Fatal("eviction damaged an active reader", string(body), err)
	}
}

func TestFileStoreIdleGCRemovesExpiredAudioWithoutAnotherRequest(t *testing.T) {
	store, err := newFileStore(t.TempDir(), 64, 30*time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	completeCachedAudio(t, store, "old", "audio", time.Now().Add(-time.Second))
	path := filepath.Join(store.dir, "old.audio")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("idle GC did not remove expired audio", err)
	}
	if record, err := store.Get(t.Context(), "old"); err != nil || record.Status != "expired" {
		t.Fatal("idle cleanup removed the paid record", record, err)
	}
}

func TestFileStoreExpiryAndFailedDeletionDeferUntilReaderCloses(t *testing.T) {
	for _, reason := range []string{"expired", "failed"} {
		t.Run(reason, func(t *testing.T) {
			store := cacheStore(t, 8, time.Hour)
			completeCachedAudio(t, store, "playing", "keep", time.Now().Add(-time.Minute))
			reader, err := store.OpenAudio(t.Context(), "playing")
			if err != nil {
				t.Fatal(err)
			}
			if reason == "expired" {
				store.Configure(8, time.Second)
				if _, err = store.OpenAudio(t.Context(), "playing"); !errors.Is(err, domain.ErrExpired) {
					t.Fatal("expired audio admitted a new playback", err)
				}
			} else if err = store.DeleteAudio(t.Context(), "playing"); err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			used := store.used
			store.mu.Unlock()
			if used != 4 {
				t.Fatal("a pinned file was prematurely deducted from disk usage", used)
			}
			if body, err := io.ReadAll(reader); err != nil || string(body) != "keep" {
				t.Fatal("cleanup interrupted the existing playback", string(body), err)
			}
			if err = reader.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(store.dir, "playing.audio")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("deferred removal was not retried on last close", err)
			}
			store.mu.Lock()
			used = store.used
			store.mu.Unlock()
			if used != 0 {
				t.Fatal("closed expired audio continued using quota", used)
			}
		})
	}
}

func TestFileStoreDeleteFailureDoesNotReleaseQuota(t *testing.T) {
	store := cacheStore(t, 8, time.Hour)
	completeCachedAudio(t, store, "audio", "keep", time.Now())
	path := filepath.Join(store.dir, "audio.audio")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "block-removal")
	if err := os.WriteFile(child, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAudio(t.Context(), "audio"); err == nil {
		t.Fatal("expected a physical removal failure")
	}
	store.mu.Lock()
	used := store.used
	store.mu.Unlock()
	if used != 4 {
		t.Fatal("failed removal released disk quota", used)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAudio(t.Context(), "audio"); err != nil {
		t.Fatal("already missing file did not release tracked quota", err)
	}
	store.mu.Lock()
	used = store.used
	store.mu.Unlock()
	if used != 0 {
		t.Fatal("missing audio remained charged against disk quota", used)
	}
}

func TestFileStoreLongCustomRetentionKeepsMetadataUntilAudioIsRemoved(t *testing.T) {
	store := cacheStore(t, 8, 90*24*time.Hour)
	completeCachedAudio(t, store, "retained", "keep", time.Now().Add(-40*24*time.Hour))
	store.mu.Lock()
	store.cleanupLocked()
	store.mu.Unlock()
	if record, err := store.Get(t.Context(), "retained"); err != nil || record.Status != "complete" {
		t.Fatal("metadata cleanup orphaned audio retained by custom policy", record, err)
	}
	store.Configure(4, 90*24*time.Hour)
	writer, err := store.CreateAudio(t.Context(), "replacement")
	if err != nil {
		t.Fatal("old retained audio was no longer eligible for pressure eviction", err)
	}
	writer.Close()
}

func TestFileStoreCloseStopsGCClosesHandlesAndReleasesOwnership(t *testing.T) {
	dir := t.TempDir()
	store, err := newFileStore(dir, 8, time.Hour, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	completeCachedAudio(t, store, "complete", "keep", time.Now())
	reader, err := store.OpenAudio(t.Context(), "complete")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.CreateAudio(t.Context(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	select {
	case <-store.gcDone:
	default:
		t.Fatal("store close left its cleanup goroutine running")
	}
	if _, err = reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatal("store close left a reader open", err)
	}
	if _, err = writer.Write([]byte("x")); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatal("closed store accepted more audio", err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal("reader cleanup after store close was not safe", err)
	}
	reopened, err := newFileStore(dir, 8, time.Hour, time.Hour)
	if err != nil {
		t.Fatal("store close retained its ownership lock", err)
	}
	reopened.Close()
}

func TestFileStoreRestartDiscardsPartialAudioAndRetainsUncertainIdentity(t *testing.T) {
	dir := t.TempDir()
	store, err := newFileStore(dir, 16, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	record := domain.Record{ID: "accepted", Operation: "tts", Status: "pending", CreatedAt: time.Now().Add(-40 * 24 * time.Hour), Fingerprint: "paid-fingerprint"}
	if err = store.Put(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	writer, err := store.CreateAudio(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := newFileStore(dir, 16, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.Get(context.Background(), record.ID)
	if err != nil || recovered.Status != "uncertain" || recovered.Fingerprint != "paid-fingerprint" {
		t.Fatal("restart lost accepted request protection", recovered, err)
	}
	if _, err = os.Stat(filepath.Join(dir, record.ID+".audio")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("restart retained unusable partial audio", err)
	}
}
