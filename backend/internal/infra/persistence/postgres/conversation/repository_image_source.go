package conversation

import (
	"context"
	"errors"
	"time"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/dberror"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func sourceAliasWhere(db *gorm.DB, userID uint, source repository.ImageSourceFingerprint) *gorm.DB {
	return db.Where("user_id = ? AND source_sha256 = ? AND source_size_bytes = ? AND image_policy_version = ?", userID, source.SourceSHA256, source.SourceSizeBytes, source.ImagePolicyVersion)
}

func activeImageSourceFile(tx *gorm.DB, userID uint, source repository.ImageSourceFingerprint) (*models.FileObject, error) {
	var alias models.ImageSourceAlias
	if err := sourceAliasWhere(tx, userID, source).First(&alias).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var file models.FileObject
	err := tx.Where("user_id = ? AND file_id = ? AND status = ? AND file_category = ? AND storage_path <> '' AND (expires_at IS NULL OR expires_at > ?)", userID, alias.FileID, "active", "image", time.Now()).First(&file).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &file, nil
}

func (r *Repo) GetActiveImageSourceFiles(ctx context.Context, userID uint, sources []repository.ImageSourceFingerprint) (map[repository.ImageSourceFingerprint]domainconversation.FileObject, error) {
	result := make(map[repository.ImageSourceFingerprint]domainconversation.FileObject, len(sources))
	if len(sources) == 0 {
		return result, nil
	}
	if len(sources) > 100 {
		return nil, repository.ErrInvalidInput
	}
	db := r.db.WithContext(ctx)
	conditions := db.Session(&gorm.Session{NewDB: true})
	for _, source := range sources {
		conditions = conditions.Or("(source_sha256 = ? AND source_size_bytes = ? AND image_policy_version = ?)", source.SourceSHA256, source.SourceSizeBytes, source.ImagePolicyVersion)
	}
	var aliases []models.ImageSourceAlias
	if err := db.Where("user_id = ?", userID).Where(conditions).Find(&aliases).Error; err != nil {
		return nil, dberror.Translate(err)
	}
	if len(aliases) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		ids = append(ids, alias.FileID)
	}
	var files []models.FileObject
	if err := db.Where("user_id = ? AND file_id IN ? AND status = ? AND file_category = ? AND storage_path <> '' AND (expires_at IS NULL OR expires_at > ?)", userID, ids, "active", "image", time.Now()).Find(&files).Error; err != nil {
		return nil, dberror.Translate(err)
	}
	byID := make(map[string]domainconversation.FileObject, len(files))
	for _, file := range files {
		byID[file.FileID] = toFileObjectDomain(file)
	}
	for _, alias := range aliases {
		if file, ok := byID[alias.FileID]; ok {
			result[repository.ImageSourceFingerprint{SourceSHA256: alias.SourceSHA256, SourceSizeBytes: alias.SourceSizeBytes, ImagePolicyVersion: alias.ImagePolicyVersion}] = file
		}
	}
	return result, nil
}

func putImageSourceAlias(tx *gorm.DB, userID uint, source repository.ImageSourceFingerprint, fileID string) error {
	alias := models.ImageSourceAlias{UserID: userID, SourceSHA256: source.SourceSHA256, SourceSizeBytes: source.SourceSizeBytes, ImagePolicyVersion: source.ImagePolicyVersion, FileID: fileID}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "source_sha256"}, {Name: "source_size_bytes"}, {Name: "image_policy_version"}}, DoUpdates: clause.Assignments(map[string]any{"file_id": fileID, "updated_at": time.Now()})}).Create(&alias).Error
}

// InvalidateImageSource removes only the candidate examined by the caller. A
// concurrent replacement must remain intact for the next lookup/transaction.
func (r *Repo) InvalidateImageSource(ctx context.Context, userID uint, source repository.ImageSourceFingerprint, expectedFileID string) error {
	return dberror.Translate(sourceAliasWhere(r.db.WithContext(ctx), userID, source).Where("file_id = ?", expectedFileID).Delete(&models.ImageSourceAlias{}).Error)
}

func (r *Repo) BindImageSource(ctx context.Context, userID uint, source repository.ImageSourceFingerprint, fileID string, quotaLimit int64) (*domainconversation.FileObject, error) {
	var result models.FileObject
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := getOrInitQuotaForUpdate(tx, userID, quotaLimit); err != nil {
			return err
		}
		if existing, err := activeImageSourceFile(tx, userID, source); err != nil {
			return err
		} else if existing != nil {
			result = *existing
			return nil
		}
		if err := tx.Where("user_id = ? AND file_id = ? AND status = ? AND file_category = ?", userID, fileID, "active", "image").First(&result).Error; err != nil {
			return err
		}
		return putImageSourceAlias(tx, userID, source, fileID)
	})
	if err != nil {
		return nil, dberror.Translate(err)
	}
	file := toFileObjectDomain(result)
	return &file, nil
}

// CreateFileObjectWithImageSourceAndConsumeQuota serializes with the existing
// per-user quota lock. A competing source upload returns the first active file
// before checking quota or inserting another FileObject.
func (r *Repo) CreateFileObjectWithImageSourceAndConsumeQuota(ctx context.Context, item *domainconversation.FileObject, source repository.ImageSourceFingerprint, quotaLimit int64) (*domainconversation.StorageQuota, bool, error) {
	var updated models.UserStorageQuota
	reused := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		quota, err := getOrInitQuotaForUpdate(tx, item.UserID, quotaLimit)
		if err != nil {
			return err
		}
		if existing, lookupErr := activeImageSourceFile(tx, item.UserID, source); lookupErr != nil {
			return lookupErr
		} else if existing != nil {
			*item = toFileObjectDomain(*existing)
			updated = *quota
			reused = true
			return nil
		}
		// Different source fingerprints may normalize to identical bytes in
		// separate server processes. The shared quota row lock must protect this
		// output-SHA check as well as source aliases; process-local gates alone
		// cannot prevent duplicate FileObjects or quota charges.
		var existingOutput models.FileObject
		lookupErr := tx.Where("user_id = ? AND sha256 = ? AND size_bytes = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)", item.UserID, item.SHA256, item.SizeBytes, "active", time.Now()).Order("id DESC").First(&existingOutput).Error
		if lookupErr == nil {
			if err = putImageSourceAlias(tx, item.UserID, source, existingOutput.FileID); err != nil {
				return err
			}
			*item = toFileObjectDomain(existingOutput)
			updated = *quota
			reused = true
			return nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}
		if quota.QuotaBytes > 0 && quota.UsedBytes+quota.ReservedBytes+item.SizeBytes > quota.QuotaBytes {
			return repository.ErrStorageQuotaExceeded
		}
		entity := toFileObjectModel(item)
		if err = tx.Create(&entity).Error; err != nil {
			return err
		}
		if err = putImageSourceAlias(tx, item.UserID, source, item.FileID); err != nil {
			return err
		}
		if err = tx.Model(&models.UserStorageQuota{}).Where("id = ?", quota.ID).Update("used_bytes", gorm.Expr("used_bytes + ?", item.SizeBytes)).Error; err != nil {
			return err
		}
		if err = tx.Where("id = ?", quota.ID).First(&updated).Error; err != nil {
			return err
		}
		*item = toFileObjectDomain(entity)
		return nil
	})
	if err != nil {
		return nil, false, dberror.Translate(err)
	}
	quota := toStorageQuotaDomain(updated)
	return &quota, reused, nil
}
