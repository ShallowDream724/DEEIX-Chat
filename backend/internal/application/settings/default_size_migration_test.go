package settings

import (
	"context"
	"testing"
	"time"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
)

func TestSeedUpdatesOnlyUntouchedSizeDefaults(t *testing.T) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	repo := newSettingsSeedRepo(
		domainsettings.SystemSetting{Namespace: "storage", Key: "max_upload_file_bytes", Value: "20971520", CreatedAt: created, UpdatedAt: created},
		domainsettings.SystemSetting{Namespace: "storage", Key: "user_storage_quota_bytes", Value: "1000000000", CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
		domainsettings.SystemSetting{Namespace: "file", Key: "generated_image_max_bytes", Value: "52428800", CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
		domainsettings.SystemSetting{Namespace: "file", Key: "file_full_context_max_bytes", Value: "2097152", CreatedAt: created, UpdatedAt: created},
		domainsettings.SystemSetting{Namespace: "file", Key: "image_attachment_processor_max_bytes", Value: "20971520"},
	)
	service := NewService(repo, nil)
	for range 2 {
		if err := service.Seed(context.Background()); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{
			"storage:max_upload_file_bytes": "20000000", "storage:user_storage_quota_bytes": "1000000000",
			"file:generated_image_max_bytes": "52428800", "file:file_full_context_max_bytes": "2000000",
			"file:image_attachment_processor_max_bytes": "20971520",
		} {
			if got := repo.items[key].Value; got != want {
				t.Errorf("%s=%s, want %s", key, got, want)
			}
		}
	}
}
