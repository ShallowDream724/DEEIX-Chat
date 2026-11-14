package llm

import (
	"context"
	"time"
)

// NativeFileRecord contains no credentials or source content. Key includes the
// authenticated user, provider credential scope, immutable source and purpose.
type NativeFileRecord struct {
	Key               string
	UserID            uint
	Scope             string
	SourceID          string
	Purpose           string
	FileName          string
	SizeBytes         int64
	ProviderFileID    string
	State             string
	ClaimToken        string
	LeaseUntil        time.Time
	ExpiresAt         time.Time
	CreatedAt         time.Time
	BillingTicketJSON string
	Pages             int64
	QuantityKnown     bool
	BillingSettled    bool
}

type NativeFileBilling interface {
	Prepare(context.Context, NativeFileRecord, int64, bool) (string, error)
	Complete(context.Context, NativeFileRecord, int64, bool) error
}

type NativeURLPage struct{ Title, Content string }
type NativeURLFetcher interface {
	FetchNativeURL(context.Context, RouteConfig, string) (NativeURLPage, error)
}

// NativeFileStore atomically claims one upload. A lease expiry permits recovery
// by listing upstream files, never a second speculative paid upload.
type NativeFileStore interface {
	Load(context.Context, string) (NativeFileRecord, bool, error)
	Claim(context.Context, NativeFileRecord) (bool, error)
	Save(context.Context, NativeFileRecord) error
}
