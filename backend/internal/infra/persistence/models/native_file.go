package models

import "time"

type NativeFile struct {
	Key               string    `gorm:"primaryKey;size:64"`
	UserID            uint      `gorm:"not null;index"`
	Scope             string    `gorm:"size:64;not null;index"`
	SourceID          string    `gorm:"size:128;not null"`
	Purpose           string    `gorm:"size:48;not null"`
	FileName          string    `gorm:"size:512;not null"`
	SizeBytes         int64     `gorm:"not null"`
	ProviderFileID    string    `gorm:"size:128;not null;default:''"`
	State             string    `gorm:"size:16;not null"`
	ClaimToken        string    `gorm:"size:64;not null"`
	LeaseUntil        time.Time `gorm:"not null"`
	ExpiresAt         time.Time `gorm:"not null;index"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
	BillingTicketJSON string `gorm:"type:text;not null;default:''"`
	Pages             int64  `gorm:"not null;default:0"`
	QuantityKnown     bool   `gorm:"not null;default:false"`
	BillingSettled    bool   `gorm:"not null;default:false"`
}

func (NativeFile) TableName() string { return "llm_native_files" }
