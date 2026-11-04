package settings

import (
	"testing"
	"time"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestSeedResetsOnlyLegacyInputLimitsShape(t *testing.T) {
	for _, raw := range []string{
		`{"profiles":{"compatible":{"requestBytes":123}},"upstreams":{"7":{"imageCount":2}}}`,
		`{"upstreams":{"7":{"protocols":{"openai_responses":{"requestBytes":123}}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			repo := newSettingsSeedRepo(
				domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: raw, CreatedAt: created, UpdatedAt: created.Add(time.Hour)},
				domainsettings.SystemSetting{Namespace: "file", Key: "file_full_context_max_bytes", Value: "777"},
				domainsettings.SystemSetting{Namespace: "custom", Key: "credential", Value: "existing-value"},
			)
			service := NewService(repo, nil)
			if err := service.Seed(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := repo.items["file:input_limits"].Value; got != inputlimits.DefaultConfigJSON() {
				t.Fatalf("legacy budget not reset: %s", got)
			}
			if repo.items["file:file_full_context_max_bytes"].Value != "777" || repo.items["custom:credential"].Value != "existing-value" {
				t.Fatal("unrelated settings changed")
			}
			if err := service.Seed(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := repo.items["file:input_limits"].Value; got != inputlimits.DefaultConfigJSON() {
				t.Fatalf("repeated seed changed new defaults: %s", got)
			}
		})
	}
}

func TestSeedKeepsCurrentInputLimitsShape(t *testing.T) {
	for _, raw := range []string{
		`{"defaults":{"requestBytes":123},"upstreams":{"7":{"imageCount":2}}}`,
		`{"upstreams":{"7":{"imageCount":2}}}`,
	} {
		repo := newSettingsSeedRepo(domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: raw})
		if err := NewService(repo, nil).Seed(t.Context()); err != nil {
			t.Fatal(err)
		}
		got := repo.items["file:input_limits"].Value
		if inputlimits.Resolve(got, "", "", 7) != inputlimits.Resolve(raw, "", "", 7) {
			t.Fatalf("current budget values were reset: %s", got)
		}
	}
}
