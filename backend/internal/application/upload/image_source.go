package upload

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	domainuser "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/user"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

// ImageSourceFingerprint is the application input/cache-key value for a
// user's declared original image. It conveys neither file access nor proof of
// the canonical image's actual bytes.
type ImageSourceFingerprint = repository.ImageSourceFingerprint

func ImagePolicyVersion(maxDimension int) string {
	if maxDimension < 0 {
		maxDimension = 0
	}
	return fmt.Sprintf("image-normalize-v1:%d", maxDimension)
}

func canonicalSourceMetadata(mimeType string, metadata imageutil.Metadata, maxDimension int) bool {
	if mimeType != "image/png" && mimeType != "image/jpeg" {
		return false
	}
	if metadata.Width <= 0 || metadata.Height <= 0 || metadata.Orientation != 1 || metadata.BitDepth <= 0 || metadata.BitDepth > 8 || metadata.Animated || !metadata.AnimationKnown {
		return false
	}
	return maxDimension <= 0 || (metadata.Width <= maxDimension && metadata.Height <= maxDimension)
}

func canonicalSourceProfile(file domainconversation.FileObject, cfg config.Config) bool {
	return canonicalSourceMetadata(file.DetectedMIME, imageutil.Metadata{Width: file.ImageWidth, Height: file.ImageHeight, Orientation: file.ImageOrientation, BitDepth: file.ImageBitDepth, Animated: file.ImageAnimated, AnimationKnown: file.ImageAnimationKnown}, cfg.ImageMaxDimension)
}

func sourceMetadataUnknown(file domainconversation.FileObject) bool {
	return file.ImageWidth <= 0 || file.ImageHeight <= 0 || file.ImageOrientation == 0 || file.ImageBitDepth <= 0 || !file.ImageAnimationKnown
}

// Content verification is independent of metadata completeness so an old
// output-SHA duplicate can be reused ordinarily, while a missing object is
// still invalidated before any metadata fallback is considered.
func (s *Service) sourceCandidateContentValid(ctx context.Context, store objectstorage.Store, userID uint, file domainconversation.FileObject) (bool, error) {
	cfg := s.snapshot()
	if file.UserID != userID || file.Status != "active" || file.FileCategory != "image" || (file.ExpiresAt != nil && !file.ExpiresAt.After(time.Now())) {
		return false, nil
	}
	if !isAllowedMIME(file.DetectedMIME, cfg) || isDangerousMIME(file.DetectedMIME) || file.SizeBytes > EffectiveMaxBytes(cfg, file.DetectedMIME, file.FileName) {
		return false, nil
	}
	matches, err := objectMatchesContent(ctx, store, file.StoragePath, file.SHA256, file.SizeBytes)
	if err != nil || matches {
		return matches, err
	}
	_, _, _, err = s.repo.DeleteFileObjectAndReleaseQuota(ctx, userID, file.FileID, cfg.UserStorageQuotaBytes, repository.DeleteFileObjectOptions{})
	if errors.Is(err, repository.ErrFileNotFound) || errors.Is(err, repository.ErrNotFound) {
		err = nil
	}
	return false, err
}

func (s *Service) bindCanonicalImageSource(ctx context.Context, store objectstorage.Store, source repository.ImageSourceFingerprint, target domainconversation.FileObject, quotaBytes int64) (*domainconversation.FileObject, error) {
	for attempt := 0; attempt < 3; attempt++ {
		if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
			return &target, nil
		}
		canonical, err := s.repo.BindImageSource(ctx, target.UserID, source, target.FileID, quotaBytes)
		if err != nil {
			return nil, err
		}
		if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
			return &target, nil
		}
		readable, err := s.sourceCandidateContentValid(ctx, store, target.UserID, *canonical)
		if err != nil {
			return nil, err
		}
		if readable && canonicalSourceProfile(*canonical, s.snapshot()) {
			if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
				return &target, nil
			}
			return canonical, nil
		}
		if err = s.repo.InvalidateImageSource(ctx, target.UserID, source, canonical.FileID); err != nil {
			return nil, err
		}
		// Older output-SHA duplicates remain ordinary uploads when their own
		// metadata is unknown. Prefer a valid first canonical if one existed,
		// but never leave a newly bound unknown target in the source cache.
		if readable && canonical.FileID == target.FileID && sourceMetadataUnknown(target) {
			return &target, nil
		}
	}
	return nil, s.errInvalidFileReference()
}

func (s *Service) createCanonicalImageSource(ctx context.Context, store objectstorage.Store, item *domainconversation.FileObject, source repository.ImageSourceFingerprint, quotaBytes int64) (*domainconversation.StorageQuota, bool, error) {
	pending := *item
	for attempt := 0; attempt < 3; attempt++ {
		*item = pending
		if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
			quota, err := s.repo.CreateFileObjectAndConsumeQuota(ctx, item, quotaBytes)
			return quota, false, err
		}
		quota, reused, err := s.repo.CreateFileObjectWithImageSourceAndConsumeQuota(ctx, item, source, quotaBytes)
		if err != nil || !reused {
			return quota, reused, err
		}
		if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
			continue
		}
		readable, err := s.sourceCandidateContentValid(ctx, store, pending.UserID, *item)
		if err != nil {
			return nil, false, err
		}
		if readable && canonicalSourceProfile(*item, s.snapshot()) {
			if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
				continue
			}
			return quota, true, nil
		}
		if err = s.repo.InvalidateImageSource(ctx, pending.UserID, source, item.FileID); err != nil {
			return nil, false, err
		}
		if readable && item.SHA256 == pending.SHA256 && item.SizeBytes == pending.SizeBytes && sourceMetadataUnknown(*item) {
			// A concurrent old uploader may have stored identical verified bytes
			// without image metadata. Keep ordinary output deduplication, but do
			// not claim that this old record is a normalized source-cache entry.
			return quota, true, nil
		}
	}
	return nil, false, s.errInvalidFileReference()
}

func (s *Service) GetStorageQuota(ctx context.Context, userID uint) (*domainconversation.StorageQuota, error) {
	return s.repo.GetOrInitUserStorageQuota(ctx, userID, s.snapshot().UserStorageQuotaBytes)
}

func validImageSource(source repository.ImageSourceFingerprint) bool {
	b, err := hex.DecodeString(source.SourceSHA256)
	return err == nil && len(b) == 32 && source.SourceSHA256 == strings.ToLower(source.SourceSHA256) && source.SourceSizeBytes > 0 && len(source.ImagePolicyVersion) <= 96
}

// LookupImageSources returns only this active user's readable canonical images.
// Stale policies and malformed cache keys are cache misses, allowing mixed
// batches from older tabs to continue with the current policy.
func (s *Service) LookupImageSources(ctx context.Context, userID uint, sources []ImageSourceFingerprint) (map[ImageSourceFingerprint]domainconversation.FileObject, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Status != domainuser.StatusActive {
		return nil, s.errInvalidFileReference()
	}
	if len(sources) > 100 {
		return nil, s.errInvalidFileReference()
	}
	cfg := s.snapshot()
	current := ImagePolicyVersion(cfg.ImageMaxDimension)
	valid := make([]repository.ImageSourceFingerprint, 0, len(sources))
	for _, source := range sources {
		if validImageSource(source) && source.ImagePolicyVersion == current {
			valid = append(valid, source)
		}
	}
	result := make(map[repository.ImageSourceFingerprint]domainconversation.FileObject)
	if len(valid) == 0 {
		return result, nil
	}
	files, err := s.repo.GetActiveImageSourceFiles(ctx, userID, valid)
	if err != nil {
		return nil, err
	}
	store, err := s.openObjectStore(ctx)
	if err != nil {
		return nil, err
	}
	verified := make(map[string]bool, len(files))
	for source, file := range files {
		if file.UserID != userID || file.Status != "active" || file.FileCategory != "image" || (file.ExpiresAt != nil && !file.ExpiresAt.After(time.Now())) {
			continue
		}
		if !canonicalSourceProfile(file, cfg) {
			continue
		}
		if !isAllowedMIME(file.DetectedMIME, cfg) || isDangerousMIME(file.DetectedMIME) || file.SizeBytes > EffectiveMaxBytes(cfg, file.DetectedMIME, file.FileName) {
			continue
		}
		if matches, seen := verified[file.FileID]; seen {
			if matches {
				result[source] = file
			}
			continue
		}
		matches, matchErr := objectMatchesContent(ctx, store, file.StoragePath, file.SHA256, file.SizeBytes)
		if matchErr != nil {
			return nil, matchErr
		}
		verified[file.FileID] = matches
		if matches {
			result[source] = file
		} else {
			// Invalidate the same stale FileObject/quota record used by ordinary
			// output-SHA deduplication, so a later upload can replace its alias.
			if _, _, _, deleteErr := s.repo.DeleteFileObjectAndReleaseQuota(ctx, userID, file.FileID, s.snapshot().UserStorageQuotaBytes, repository.DeleteFileObjectOptions{}); deleteErr != nil && !errors.Is(deleteErr, repository.ErrFileNotFound) && !errors.Is(deleteErr, repository.ErrNotFound) {
				return nil, deleteErr
			}
		}
	}
	if current != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
		return map[repository.ImageSourceFingerprint]domainconversation.FileObject{}, nil
	}
	return result, nil
}

func (s *Service) acceptedImageSource(input UploadFileInput) (*repository.ImageSourceFingerprint, error) {
	if input.SourceSHA256 == "" && input.SourceSizeBytes == 0 && input.ImagePolicyVersion == "" {
		return nil, nil
	}
	source := repository.ImageSourceFingerprint{SourceSHA256: strings.ToLower(strings.TrimSpace(input.SourceSHA256)), SourceSizeBytes: input.SourceSizeBytes, ImagePolicyVersion: strings.TrimSpace(input.ImagePolicyVersion)}
	if !validImageSource(source) || input.Ownership == FileOwnershipSystem {
		return nil, s.errInvalidFileReference()
	}
	// Upload remains valid when an administrator changes policy while processing
	// was in flight, but the obsolete source key is never registered.
	if source.ImagePolicyVersion != ImagePolicyVersion(s.snapshot().ImageMaxDimension) {
		return nil, nil
	}
	return &source, nil
}
