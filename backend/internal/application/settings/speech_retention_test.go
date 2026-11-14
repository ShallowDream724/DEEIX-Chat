package settings

import (
	"testing"
	"time"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
)

func TestSpeechRetentionUpgradeOnlyChangesUntouchedDefault(t *testing.T) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, stored, want string
		edited             bool
	}{
		{"old default", "72", "6", false},
		{"saved old duration", "72", "72", true},
		{"custom duration", "12", "12", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := created
			if tc.edited {
				updated = created.Add(time.Minute)
			}
			repo := newSettingsSeedRepo(domain.SystemSetting{Namespace: "speech", Key: "cache_ttl_hours", Value: tc.stored, CreatedAt: created, UpdatedAt: updated})
			s := NewService(repo, nil)
			for range 2 {
				if err := s.Seed(t.Context()); err != nil {
					t.Fatal(err)
				}
				if got := repo.items["speech:cache_ttl_hours"].Value; got != tc.want {
					t.Fatalf("got %s, want %s", got, tc.want)
				}
				if repo.items["speech:cache_max_bytes"].Value != "268435456" {
					t.Fatal("global audio quota changed")
				}
			}
		})
	}
}
