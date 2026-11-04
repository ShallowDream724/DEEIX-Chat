package settings

import (
	"testing"
	"time"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestInputBudgetMigrationPreservesSavedLimitsAndLegacyCount(t *testing.T) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	repo := newSettingsSeedRepo(
		domainsettings.SystemSetting{Namespace: "storage", Key: "max_message_files", Value: "24"},
		domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: `{"defaults":{"imageCount":0,"requestBytes":120000000},"upstreams":{"7":{"messageFileCount":5,"imageCount":3}}}`, CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
		domainsettings.SystemSetting{Namespace: "file", Key: "full_context_max_tokens", Value: "272000", CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
	)
	service := NewService(repo, nil)
	for range 2 {
		if err := service.Seed(t.Context()); err != nil {
			t.Fatal(err)
		}
		raw := repo.items["file:input_limits"].Value
		global := inputlimits.Resolve(raw, "", "", 0)
		channel := inputlimits.Resolve(raw, "", "", 7)
		if global.MessageFileCount != 24 || global.ImageCount != 0 || global.RequestBytes != 120_000_000 || channel.MessageFileCount != 5 || channel.ImageCount != 3 {
			t.Fatalf("saved limits lost: global=%+v channel=%+v", global, channel)
		}
		if repo.items["file:full_context_max_tokens"].Value != "272000" {
			t.Fatal("saved full-text budget changed")
		}
	}
}

func TestInputBudgetMigrationOnlyRaisesUntouchedZeroDefaults(t *testing.T) {
	const old = `{"defaults":{"requestBytes":100000000,"imageCount":0,"imageBytes":0,"imageSingleBytes":0,"nativeCount":0,"nativeBytes":0,"pdfSingleBytes":0,"pdfBytes":0,"pdfPages":0,"nonPDFBytes":0},"upstreams":{}}`
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, edited := range []bool{false, true} {
		updated := created
		if edited {
			updated = created.Add(time.Hour)
		}
		repo := newSettingsSeedRepo(domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: old, CreatedAt: created, UpdatedAt: updated})
		if err := NewService(repo, nil).Seed(t.Context()); err != nil {
			t.Fatal(err)
		}
		got := inputlimits.Resolve(repo.items["file:input_limits"].Value, "", "", 0)
		if edited && got.ImageCount != 0 {
			t.Fatal("explicit unlimited setting overwritten")
		}
		if !edited && (got.ImageCount != 20 || got.NativeBytes != 50_000_000 || got.PDFPages != 200) {
			t.Fatalf("old seed left unprotected: %+v", got)
		}
	}
}

// The published 56b0e8db image had storage.max_message_files, but no
// file.input_limits row. Exercise that upgrade rather than a fresh install.
func TestUpgradePublishedNativeFilesImageSeedsInputBudgets(t *testing.T) {
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		stored string
		count  int
	}{{"10", 10}, {"24", 24}} {
		t.Run(tc.stored, func(t *testing.T) {
			repo := newSettingsSeedRepo(
				domainsettings.SystemSetting{Namespace: "storage", Key: "max_message_files", Value: tc.stored},
				domainsettings.SystemSetting{Namespace: "storage", Key: "max_upload_file_bytes", Value: "50000000", CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
				domainsettings.SystemSetting{Namespace: "file", Key: "full_context_max_tokens", Value: "272000", CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
			)
			service := NewService(repo, nil)
			want := inputlimits.Limits{
				MessageFileCount: tc.count, RequestBytes: 100_000_000,
				ImageCount: 20, ImageSingleBytes: 5_000_000, ImageBytes: 20_000_000,
				NativeCount: 10, NativeBytes: 50_000_000, PDFSingleBytes: 50_000_000,
				PDFBytes: 50_000_000, PDFPages: 200, NonPDFBytes: 50_000_000,
			}
			for range 2 {
				if err := service.Seed(t.Context()); err != nil {
					t.Fatal(err)
				}
				item := repo.items["file:input_limits"]
				if got := inputlimits.Resolve(item.Value, "", "", 0); got != want {
					t.Fatalf("published image upgrade left incorrect budgets: %+v", got)
				}
				if got := inputlimits.Resolve(service.settingResponse(item).Value, "", "", 0); got != want {
					t.Fatalf("admin response differs from effective budgets: %+v", got)
				}
				if repo.items["storage:max_upload_file_bytes"].Value != "50000000" || repo.items["file:full_context_max_tokens"].Value != "272000" {
					t.Fatal("administrator's existing file settings were reset")
				}
			}
		})
	}
}
