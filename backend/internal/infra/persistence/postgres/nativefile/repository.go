package nativefile

import (
	"context"
	"errors"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/persistence/models"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repo struct{ db *gorm.DB }

func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

func (r *Repo) Load(ctx context.Context, key string) (llm.NativeFileRecord, bool, error) {
	var row models.NativeFile
	err := r.db.WithContext(ctx).Where("key = ?", key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return llm.NativeFileRecord{}, false, nil
	}
	return toRecord(row), err == nil, err
}

func (r *Repo) Claim(ctx context.Context, record llm.NativeFileRecord) (bool, error) {
	row := toModel(record)
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil || result.RowsAffected == 1 {
		return result.RowsAffected == 1, result.Error
	}
	// Reuse an expired completed record or acquire recovery ownership. Preserve
	// pending/uncertain state so the caller must reconcile before any upload.
	result = r.db.WithContext(ctx).Model(&models.NativeFile{}).
		Where("key = ? AND user_id = ? AND scope = ? AND lease_until <= ?", record.Key, record.UserID, record.Scope, time.Now()).
		Updates(map[string]any{"claim_token": record.ClaimToken, "lease_until": record.LeaseUntil})
	return result.RowsAffected == 1, result.Error
}

func (r *Repo) Save(ctx context.Context, record llm.NativeFileRecord) error {
	row := toModel(record)
	result := r.db.WithContext(ctx).Model(&models.NativeFile{}).
		Where("key = ? AND user_id = ? AND scope = ? AND claim_token = ?", record.Key, record.UserID, record.Scope, record.ClaimToken).
		Updates(map[string]any{"provider_file_id": row.ProviderFileID, "state": row.State, "expires_at": row.ExpiresAt, "lease_until": row.LeaseUntil, "file_name": row.FileName, "billing_ticket_json": row.BillingTicketJSON, "pages": row.Pages, "quantity_known": row.QuantityKnown, "billing_settled": row.BillingSettled})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("native file claim lost")
	}
	return nil
}

func toModel(r llm.NativeFileRecord) models.NativeFile {
	return models.NativeFile{Key: r.Key, UserID: r.UserID, Scope: r.Scope, SourceID: r.SourceID, Purpose: r.Purpose, FileName: r.FileName, SizeBytes: r.SizeBytes, ProviderFileID: r.ProviderFileID, State: r.State, ClaimToken: r.ClaimToken, LeaseUntil: r.LeaseUntil, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt, BillingTicketJSON: r.BillingTicketJSON, Pages: r.Pages, QuantityKnown: r.QuantityKnown, BillingSettled: r.BillingSettled}
}
func toRecord(r models.NativeFile) llm.NativeFileRecord {
	return llm.NativeFileRecord{Key: r.Key, UserID: r.UserID, Scope: r.Scope, SourceID: r.SourceID, Purpose: r.Purpose, FileName: r.FileName, SizeBytes: r.SizeBytes, ProviderFileID: r.ProviderFileID, State: r.State, ClaimToken: r.ClaimToken, LeaseUntil: r.LeaseUntil, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt, BillingTicketJSON: r.BillingTicketJSON, Pages: r.Pages, QuantityKnown: r.QuantityKnown, BillingSettled: r.BillingSettled}
}
