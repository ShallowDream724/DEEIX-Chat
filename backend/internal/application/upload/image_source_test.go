package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"testing"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type testImageAliasKey struct {
	user   uint
	source repository.ImageSourceFingerprint
}

func (r *uploadTestRepo) aliases() map[testImageAliasKey]string {
	if r.imageAliases == nil {
		r.imageAliases = make(map[testImageAliasKey]string)
	}
	return r.imageAliases
}
func (r *uploadTestRepo) GetActiveImageSourceFiles(_ context.Context, userID uint, sources []repository.ImageSourceFingerprint) (map[repository.ImageSourceFingerprint]domainconversation.FileObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[repository.ImageSourceFingerprint]domainconversation.FileObject)
	for _, source := range sources {
		id := r.aliases()[testImageAliasKey{userID, source}]
		for _, file := range r.files {
			if file.FileID == id && file.UserID == userID && file.Status == "active" {
				result[source] = file
			}
		}
	}
	return result, nil
}
func (r *uploadTestRepo) BindImageSource(_ context.Context, userID uint, source repository.ImageSourceFingerprint, fileID string, _ int64) (*domainconversation.FileObject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing := r.aliases()[testImageAliasKey{userID, source}]; existing != "" {
		for _, file := range r.files {
			if file.FileID == existing && file.UserID == userID && file.Status == "active" {
				return &file, nil
			}
		}
	}
	r.aliases()[testImageAliasKey{userID, source}] = fileID
	for _, file := range r.files {
		if file.FileID == fileID && file.UserID == userID && file.Status == "active" {
			return &file, nil
		}
	}
	return nil, repository.ErrNotFound
}
func (r *uploadTestRepo) InvalidateImageSource(_ context.Context, userID uint, source repository.ImageSourceFingerprint, expectedFileID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := testImageAliasKey{userID, source}
	if r.aliases()[key] == expectedFileID {
		delete(r.imageAliases, key)
	}
	return nil
}
func (r *uploadTestRepo) CreateFileObjectWithImageSourceAndConsumeQuota(ctx context.Context, item *domainconversation.FileObject, source repository.ImageSourceFingerprint, limit int64) (*domainconversation.StorageQuota, bool, error) {
	files, err := r.GetActiveImageSourceFiles(ctx, item.UserID, []repository.ImageSourceFingerprint{source})
	if err != nil {
		return nil, false, err
	}
	if file, ok := files[source]; ok {
		*item = file
		quota, err := r.GetOrInitUserStorageQuota(ctx, item.UserID, limit)
		return quota, true, err
	}
	quota, err := r.CreateFileObjectAndConsumeQuota(ctx, item, limit)
	if err != nil {
		return nil, false, err
	}
	_, err = r.BindImageSource(ctx, item.UserID, source, item.FileID, limit)
	return quota, false, err
}

func imageSourceInput(t *testing.T) UploadFileInput {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	source := sha256.Sum256([]byte("original-image"))
	return UploadFileInput{UserID: 1, FileName: "canonical.png", MimeType: "image/png", DeclaredSize: int64(data.Len()), Reader: bytes.NewReader(data.Bytes()), SourceSHA256: hex.EncodeToString(source[:]), SourceSizeBytes: 1234, ImagePolicyVersion: ImagePolicyVersion(0)}
}

func TestImageSourceReuseIsPrivateAndRecoversResponseLoss(t *testing.T) {
	repo := newUploadTestRepo()
	store := newUploadTestStore()
	service := newUploadTestService(repo, store)
	input := imageSourceInput(t)
	input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
	first, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.File.ImageWidth != 3 || first.File.ImageHeight != 2 || !first.File.ImageAnimationKnown {
		t.Fatalf("headers missing: %+v", first.File)
	}
	source := repository.ImageSourceFingerprint{SourceSHA256: input.SourceSHA256, SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: input.ImagePolicyVersion}
	before := repo.quota.UsedBytes
	second, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.File.FileID != first.File.FileID || repo.quota.UsedBytes != before {
		t.Fatal("response-loss retry must reuse the same quota/file")
	}
	hits, err := service.LookupImageSources(context.Background(), 2, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("cross-user source alias leaked")
	}
	old := source
	old.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension + 1)
	hits, err = service.LookupImageSources(context.Background(), 1, []repository.ImageSourceFingerprint{old})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("changed policy must miss")
	}
	if _, err = service.DeleteFile(context.Background(), 1, first.File.FileID); err != nil {
		t.Fatal(err)
	}
	hits, err = service.LookupImageSources(context.Background(), 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("deleted file must miss")
	}
}

func TestImageSourceMissingObjectDoesNotHit(t *testing.T) {
	repo := newUploadTestRepo()
	store := newUploadTestStore()
	service := newUploadTestService(repo, store)
	input := imageSourceInput(t)
	input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
	first, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(context.Background(), first.File.StoragePath); err != nil {
		t.Fatal(err)
	}
	source := repository.ImageSourceFingerprint{SourceSHA256: input.SourceSHA256, SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: input.ImagePolicyVersion}
	hits, err := service.LookupImageSources(context.Background(), 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("missing object must miss")
	}
	if repo.quota.UsedBytes != 0 {
		t.Fatal("stale object quota should be released")
	}
	input = imageSourceInput(t)
	input.ImagePolicyVersion = source.ImagePolicyVersion
	next, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if next.File.FileID == first.File.FileID {
		t.Fatal("missing object must be replaceable")
	}
}

func TestDifferentSourcesWithIdenticalOutputReuseOneFileAndQuota(t *testing.T) {
	repo := newUploadTestRepo()
	store := newUploadTestStore()
	service := newUploadTestService(repo, store)
	input := imageSourceInput(t)
	input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
	first, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := imageSourceInput(t)
	secondInput.ImagePolicyVersion = input.ImagePolicyVersion
	sourceHash := sha256.Sum256([]byte("other original"))
	secondInput.SourceSHA256 = hex.EncodeToString(sourceHash[:])
	second, err := service.UploadFile(context.Background(), secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused || second.File.FileID != first.File.FileID || repo.quota.UsedBytes != first.File.SizeBytes {
		t.Fatal("different sources with identical canonical bytes must retain output deduplication")
	}
	sources := []repository.ImageSourceFingerprint{{SourceSHA256: input.SourceSHA256, SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: input.ImagePolicyVersion}, {SourceSHA256: secondInput.SourceSHA256, SourceSizeBytes: secondInput.SourceSizeBytes, ImagePolicyVersion: secondInput.ImagePolicyVersion}}
	hits, err := service.LookupImageSources(context.Background(), 1, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected both aliases: %d", len(hits))
	}
	if err = store.Delete(context.Background(), first.File.StoragePath); err != nil {
		t.Fatal(err)
	}
	hits, err = service.LookupImageSources(context.Background(), 1, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatal("stale aliases must both miss")
	}
}

// lateImageSourceRepo publishes another upload's alias immediately after this
// request's prelookup. This reproduces the race without scheduler dependence.
type lateImageSourceRepo struct {
	*uploadTestRepo
	canonicalID     string
	lookupPublished bool
	failCreate      bool
}

func (r *lateImageSourceRepo) GetActiveImageSourceFiles(ctx context.Context, userID uint, sources []repository.ImageSourceFingerprint) (map[repository.ImageSourceFingerprint]domainconversation.FileObject, error) {
	files, err := r.uploadTestRepo.GetActiveImageSourceFiles(ctx, userID, sources)
	if err == nil && !r.lookupPublished && len(sources) > 0 {
		r.mu.Lock()
		r.aliases()[testImageAliasKey{userID, sources[0]}] = r.canonicalID
		r.lookupPublished = true
		r.mu.Unlock()
	}
	return files, err
}
func (r *lateImageSourceRepo) CreateFileObjectWithImageSourceAndConsumeQuota(ctx context.Context, item *domainconversation.FileObject, source repository.ImageSourceFingerprint, limit int64) (*domainconversation.StorageQuota, bool, error) {
	if r.failCreate {
		r.failCreate = false
		return nil, false, repository.ErrDuplicate
	}
	return r.uploadTestRepo.CreateFileObjectWithImageSourceAndConsumeQuota(ctx, item, source, limit)
}
func sizedImageInput(t *testing.T, width int, withSource bool) UploadFileInput {
	t.Helper()
	input := imageSourceInput(t)
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, width, 2))); err != nil {
		t.Fatal(err)
	}
	input.Reader = bytes.NewReader(data.Bytes())
	input.DeclaredSize = int64(data.Len())
	if !withSource {
		input.SourceSHA256 = ""
		input.SourceSizeBytes = 0
		input.ImagePolicyVersion = ""
	}
	return input
}
func TestImageSourceBindingReturnsFirstCanonicalAfterLookupRace(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("create_conflict_%v", conflict), func(t *testing.T) {
			repo := newUploadTestRepo()
			service := newUploadTestService(repo, newUploadTestStore())
			first, err := service.UploadFile(context.Background(), sizedImageInput(t, 3, false))
			if err != nil {
				t.Fatal(err)
			}
			second, err := service.UploadFile(context.Background(), sizedImageInput(t, 4, false))
			if err != nil {
				t.Fatal(err)
			}
			if first.File.FileID == second.File.FileID {
				t.Fatal("test requires different outputs")
			}
			service.repo = &lateImageSourceRepo{uploadTestRepo: repo, canonicalID: first.File.FileID, failCreate: conflict}
			repo.missNextDuplicateLookup = conflict
			input := sizedImageInput(t, 4, true)
			input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
			quotaBefore := repo.quota.UsedBytes
			result, err := service.UploadFile(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.File.FileID != first.File.FileID || !result.Reused {
				t.Fatalf("must return first source canonical: got %s want %s", result.File.FileID, first.File.FileID)
			}
			if result.Quota.UsedBytes != quotaBefore {
				t.Fatal("binding source alias changed quota")
			}
		})
	}
}

func TestImageSourceTransactionDoesNotReuseLateOversizedCanonical(t *testing.T) {
	repo := newUploadTestRepo()
	service := newUploadTestService(repo, newUploadTestStore())
	cfg := service.snapshot()
	cfg.ImageMaxDimension = 10
	service.cfg.Store(cfg)
	first, err := service.UploadFile(context.Background(), sizedImageInput(t, 3, false))
	if err != nil {
		t.Fatal(err)
	}
	repo.files[0].ImageWidth = service.snapshot().ImageMaxDimension + 1
	service.repo = &lateImageSourceRepo{uploadTestRepo: repo, canonicalID: first.File.FileID}
	input := sizedImageInput(t, 4, true)
	input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
	result, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.File.FileID == first.File.FileID {
		t.Fatal("late candidate that exceeds current dimensions must be replaced")
	}
	source := repository.ImageSourceFingerprint{SourceSHA256: input.SourceSHA256, SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: input.ImagePolicyVersion}
	hits, err := service.LookupImageSources(context.Background(), 1, []repository.ImageSourceFingerprint{source})
	if err != nil {
		t.Fatal(err)
	}
	if hits[source].FileID != result.File.FileID {
		t.Fatal("new valid canonical must replace invalid alias")
	}
}

func TestImageSourceLookupRejectsNoncanonicalAndUnknownMetadata(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*domainconversation.FileObject)
	}{
		{"unknown", func(f *domainconversation.FileObject) { f.ImageWidth = 0; f.ImageAnimationKnown = false }},
		{"animated", func(f *domainconversation.FileObject) { f.ImageAnimated = true }},
		{"unknown_animation", func(f *domainconversation.FileObject) { f.ImageAnimationKnown = false }},
		{"orientation", func(f *domainconversation.FileObject) { f.ImageOrientation = 6 }},
		{"unknown_orientation", func(f *domainconversation.FileObject) { f.ImageOrientation = 0 }},
		{"high_bit_depth", func(f *domainconversation.FileObject) { f.ImageBitDepth = 16 }},
		{"unknown_bit_depth", func(f *domainconversation.FileObject) { f.ImageBitDepth = 0 }},
		{"unsupported_format", func(f *domainconversation.FileObject) { f.DetectedMIME = "image/webp" }},
		{"oversized", func(f *domainconversation.FileObject) { f.ImageWidth = 11 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newUploadTestRepo()
			service := newUploadTestService(repo, newUploadTestStore())
			cfg := service.snapshot()
			cfg.ImageMaxDimension = 10
			service.cfg.Store(cfg)
			input := sizedImageInput(t, 3, true)
			input.ImagePolicyVersion = ImagePolicyVersion(10)
			if _, err := service.UploadFile(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			test.modify(&repo.files[0])
			source := repository.ImageSourceFingerprint{SourceSHA256: input.SourceSHA256, SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: input.ImagePolicyVersion}
			hits, err := service.LookupImageSources(context.Background(), 1, []repository.ImageSourceFingerprint{source})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 0 {
				t.Fatal("noncanonical metadata must miss")
			}
		})
	}
}

func TestImageSourceOversizedUploadRemainsStoredWithoutAlias(t *testing.T) {
	repo := newUploadTestRepo()
	service := newUploadTestService(repo, newUploadTestStore())
	cfg := service.snapshot()
	cfg.ImageMaxDimension = 2
	service.cfg.Store(cfg)
	input := sizedImageInput(t, 3, true)
	input.ImagePolicyVersion = ImagePolicyVersion(2)
	result, err := service.UploadFile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.File.ImageWidth != 3 || len(repo.imageAliases) != 0 {
		t.Fatal("noncanonical upload must save normally without a normalized alias")
	}
}

// lateOrdinaryOutputRepo models the transaction's output-SHA recheck finding
// an old uploader's record after the application's earlier output lookup missed.
type lateOrdinaryOutputRepo struct {
	*uploadTestRepo
	createCalls int
}

func (r *lateOrdinaryOutputRepo) CreateFileObjectWithImageSourceAndConsumeQuota(ctx context.Context, item *domainconversation.FileObject, source repository.ImageSourceFingerprint, limit int64) (*domainconversation.StorageQuota, bool, error) {
	r.createCalls++
	existing, err := r.GetLatestActiveFileObjectBySHA(ctx, item.UserID, item.SHA256, item.SizeBytes)
	if err != nil {
		return nil, false, err
	}
	if existing == nil {
		return r.uploadTestRepo.CreateFileObjectWithImageSourceAndConsumeQuota(ctx, item, source, limit)
	}
	canonical, err := r.BindImageSource(ctx, item.UserID, source, existing.FileID, limit)
	if err != nil {
		return nil, false, err
	}
	*item = *canonical
	quota, err := r.GetOrInitUserStorageQuota(ctx, item.UserID, limit)
	return quota, true, err
}

func TestImageSourceTransactionReusesLateOrdinaryOutputWithoutAlias(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing_object_%v", missing), func(t *testing.T) {
			repo := newUploadTestRepo()
			store := newUploadTestStore()
			service := newUploadTestService(repo, store)
			first, err := service.UploadFile(context.Background(), sizedImageInput(t, 3, false))
			if err != nil {
				t.Fatal(err)
			}
			repo.files[0].ImageWidth = 0
			repo.files[0].ImageHeight = 0
			repo.files[0].ImageOrientation = 0
			repo.files[0].ImageBitDepth = 0
			repo.files[0].ImageAnimationKnown = false
			if missing {
				if err = store.Delete(context.Background(), first.File.StoragePath); err != nil {
					t.Fatal(err)
				}
			}
			late := &lateOrdinaryOutputRepo{uploadTestRepo: repo}
			service.repo = late
			repo.missNextDuplicateLookup = true
			input := sizedImageInput(t, 3, true)
			input.ImagePolicyVersion = ImagePolicyVersion(service.snapshot().ImageMaxDimension)
			result, err := service.UploadFile(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !missing {
				if !result.Reused || result.File.FileID != first.File.FileID {
					t.Fatal("readable ordinary output duplicate must be reused")
				}
				if late.createCalls != 1 || len(repo.imageAliases) != 0 {
					t.Fatal("unknown metadata must finish without retry or source alias")
				}
			} else {
				if result.Reused || result.File.FileID == first.File.FileID {
					t.Fatal("unknown metadata must not mask a missing object")
				}
				if late.createCalls != 2 {
					t.Fatalf("expected invalidation then replacement, got %d calls", late.createCalls)
				}
			}
			if result.Quota.UsedBytes != result.File.SizeBytes {
				t.Fatal("ordinary reuse or stale replacement must charge only one output")
			}
		})
	}
}
