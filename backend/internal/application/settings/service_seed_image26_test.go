package settings

import (
	"context"
	"encoding/json"
	"testing"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestSeedMigratesImage26DefaultOptionsAndPreservesCustomPolicies(t *testing.T) {
	for _, name := range []string{"previous exact default", "older known default", "custom allowlist"} {
		t.Run(name, func(t *testing.T) {
			previous := map[string][]string{}
			if err := json.Unmarshal([]byte(config.DefaultModelOptionAllowedPathsJSON()), &previous); err != nil {
				t.Fatal(err)
			}
			for _, protocol := range []string{"openai_image_generations", "openai_image_edits"} {
				for _, path := range []string{"complexity", "frame_count", "frame_duration_ms", "safety_identifier"} {
					previous[protocol] = removeStringValue(previous[protocol], path)
				}
			}
			previous["openai_image_edits"] = removeStringValue(previous["openai_image_edits"], "moderation")
			switch name {
			case "older known default":
				delete(previous, "xai_video")
				previous["xai_responses"] = []string{"reasoning.effort"}
			case "custom allowlist":
				previous["openai_image_generations"] = removeStringValue(previous["openai_image_generations"], "quality")
			}
			encoded, err := json.Marshal(previous)
			if err != nil {
				t.Fatal(err)
			}
			customDenied := `{"default":["complexity","frame_count"]}`
			repo := newSettingsSeedRepo(
				domainsettings.SystemSetting{Namespace: "chat", Key: "model_option_allowed_paths", Value: string(encoded), ValueType: "json"},
				domainsettings.SystemSetting{Namespace: "chat", Key: "model_option_denied_paths", Value: customDenied, ValueType: "json"},
			)
			if err := NewService(repo, nil).Seed(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := config.DefaultModelOptionAllowedPathsJSON()
			if name == "custom allowlist" {
				want = string(encoded)
			}
			if got := repo.items["chat:model_option_allowed_paths"].Value; got != want {
				t.Fatalf("unexpected migrated allowlist: got %s, want %s", got, want)
			}
			if got := repo.items["chat:model_option_denied_paths"].Value; got != customDenied {
				t.Fatalf("custom denylist changed: got %s", got)
			}
		})
	}
}
