package settings

import (
	"errors"
	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"testing"
)

func TestSpeechProviderMigrationAlwaysDefaultsBrowser(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values map[string]string
		want   string
		tts    string
	}{{"fresh", nil, "browser", "false"}, {"empty choice", map[string]string{"input_provider": ""}, "browser", "false"}, {"invalid choice", map[string]string{"input_provider": "unknown"}, "browser", "false"}, {"single legacy", map[string]string{"enabled": "true", "deepseek_asr_enabled": "true"}, "browser", "false"}, {"multiple legacy", map[string]string{"enabled": "true", "doubao_enabled": "true", "wetype_enabled": "true"}, "browser", "false"}, {"explicit new choice", map[string]string{"input_provider": "wetype", "enabled": "true", "deepseek_asr_enabled": "true"}, "wetype", "false"}, {"disabled legacy TTS", map[string]string{"enabled": "false", "deepseek_tts_enabled": "true"}, "browser", "false"}} {
		t.Run(tt.name, func(t *testing.T) {
			var rows []domain.SystemSetting
			for key, value := range tt.values {
				rows = append(rows, domain.SystemSetting{Namespace: "speech", Key: key, Value: value, ValueType: "string"})
			}
			repo := newSettingsSeedRepo(rows...)
			s := NewService(repo, nil)
			if e := s.Seed(t.Context()); e != nil {
				t.Fatal(e)
			}
			if got := repo.items["speech:input_provider"].Value; got != tt.want {
				t.Fatal(got, tt.want)
			}
			if repo.items["speech:charge_asr"].Value != "false" || repo.items["speech:charge_tts"].Value != "false" {
				t.Fatal("migration enabled user charges")
			}
			for _, key := range []string{"enabled", "doubao_enabled", "wetype_enabled", "deepseek_asr_enabled", "input_provider_migration_required"} {
				if _, ok := repo.items["speech:"+key]; ok {
					t.Fatal("legacy flag remains", key)
				}
			}
			if got := repo.items["speech:deepseek_tts_enabled"].Value; got != tt.tts {
				t.Fatal("TTS gate changed unexpectedly", got)
			}
		})
	}
}
func TestSpeechSelectionRequiresIndependentKeyAndClearDisablesConsumers(t *testing.T) {
	s, _, _ := newNativeToolPricingTestService()
	for _, patch := range []PatchItem{{Namespace: "speech", Key: "input_provider", Value: "deepseek"}, {Namespace: "speech", Key: "deepseek_tts_enabled", Value: "true"}} {
		_, e := s.BatchUpdate(t.Context(), []PatchItem{patch})
		var validation *SettingValidationError
		if !errors.As(e, &validation) || validation.Details().Field != "speech:deepseek_api_key" || validation.Details().Rule != "required" {
			t.Fatal("missing independent speech key was accepted or misplaced", e)
		}
	}
	if _, e := s.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "input_provider", Value: "deepseek"}, {Namespace: "speech", Key: "deepseek_api_key", Value: "fake-independent-speech-key"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_api_key", Value: ""}}); e != nil {
		t.Fatal("blank save failed to preserve credential", e)
	}
	if _, e := s.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_api_key", Clear: true}}); e == nil {
		t.Fatal("active DeepSeek input allowed key clear")
	}
	if _, e := s.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "input_provider", Value: "browser"}, {Namespace: "speech", Key: "deepseek_tts_enabled", Value: "false"}, {Namespace: "speech", Key: "deepseek_api_key", Clear: true}}); e != nil {
		t.Fatal(e)
	}
	var runtime RuntimeSettings
	var cfg config.Config
	for _, spec := range speechSettingSpecs() {
		runtime.applyItem(&cfg, spec.seedSetting())
	}
	if cfg.Speech.InputProvider != "browser" || cfg.Speech.ChargeASR || cfg.Speech.ChargeTTS {
		t.Fatal("speech defaults do not preserve free browser input")
	}
}
