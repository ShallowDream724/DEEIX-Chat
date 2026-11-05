package repository

import (
	"context"
	"time"
)

type AttachmentSourceKind string

const AttachmentGrantMaxTTL = 3 * time.Hour

const (
	AttachmentSourceStored    AttachmentSourceKind = "stored"
	AttachmentSourcePrepared  AttachmentSourceKind = "prepared"
	AttachmentSourceTemporary AttachmentSourceKind = "temporary"
)

// AttachmentGrant binds a bearer digest to one immutable source and one run.
// Tokens themselves are never persisted. StageName is private server metadata.
type AttachmentGrant struct {
	SourceKind   AttachmentSourceKind `json:"sourceKind"`
	UserID       uint                 `json:"userID"`
	FileID       string               `json:"fileID,omitempty"`
	SourceSHA256 string               `json:"sourceSHA256,omitempty"`
	SourceSize   int64                `json:"sourceSize"`
	SourcePath   string               `json:"sourcePath,omitempty"`
	RunID        string               `json:"runID"`
	StageName    string               `json:"stageName,omitempty"`
	SHA256       string               `json:"sha256"`
	SizeBytes    int64                `json:"sizeBytes"`
	MIMEType     string               `json:"mimeType"`
	FileName     string               `json:"fileName"`
	ExpiresAt    time.Time            `json:"expiresAt"`
}

// AttachmentGrantRepository permits repeated reads until expiry or run revocation.
type AttachmentGrantRepository interface {
	PutAttachmentGrant(context.Context, string, AttachmentGrant, time.Duration) error
	GetAttachmentGrant(context.Context, string) (*AttachmentGrant, error)
	RevokeAttachmentRun(context.Context, string) ([]AttachmentGrant, error)
}
