package settings

import (
	"errors"
	"testing"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestSpeechRoleConnectionsMigrateEncryptedLegacyOnce(t *testing.T) {
	for _, owner := range []string{"asr", "tts"} {
		t.Run(owner, func(t *testing.T) {
			repo := newSettingsSeedRepo()
			s := NewService(repo, testKeyring())
			input := "deepseek"
			if owner == "tts" {
				input = "browser"
			}
			items, err := s.encryptSettingsForStorage([]domain.SystemSetting{
				{Namespace: "speech", Key: "input_provider", Value: input},
				{Namespace: "speech", Key: "deepseek_tts_enabled", Value: "true"},
				{Namespace: "speech", Key: "deepseek_base_url", Value: "https://speech.example.test"},
				{Namespace: "speech", Key: "deepseek_api_key", Value: "legacy-test-secret"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = repo.Upsert(t.Context(), items); err != nil {
				t.Fatal(err)
			}
			if err = s.Seed(t.Context()); err != nil {
				t.Fatal(err)
			}
			peer := "tts"
			if owner == "tts" {
				peer = "asr"
			}
			if repo.items["speech:deepseek_"+peer+"_credentials_source"].Value != owner {
				t.Fatal("legacy consumers no longer share one connection")
			}
			secret := repo.items["speech:deepseek_"+owner+"_api_key"]
			if secret.Value == "legacy-test-secret" || !s.settingResponse(secret).Configured || s.settingResponse(secret).Value != "" {
				t.Fatal("migrated secret exposed")
			}
			runtime := config.NewRuntime(config.Config{})
			if err = NewRuntimeSettings(repo, nil, testKeyring()).ApplyTo(t.Context(), runtime); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"asr", "tts"} {
				url, key := runtime.Snapshot().Speech.DeepSeekConnection(role)
				if url != "https://speech.example.test" || key != "legacy-test-secret" {
					t.Fatal("migration lost effective connection", role)
				}
			}
			// An explicit clear must not resurrect the old shared secret next boot.
			secret.Value = ""
			repo.items["speech:deepseek_"+owner+"_api_key"] = secret
			if err = s.Seed(t.Context()); err != nil {
				t.Fatal(err)
			}
			if repo.items["speech:deepseek_"+owner+"_api_key"].Value != "" {
				t.Fatal("migration overwrote new configuration")
			}
		})
	}
}

func TestSpeechRoleConnectionValidationAndPeerDisable(t *testing.T) {
	s, _, _ := newNativeToolPricingTestService()
	patch := func(values map[string]string) error {
		items := []PatchItem{}
		for key, value := range values {
			items = append(items, PatchItem{Namespace: "speech", Key: key, Value: value})
		}
		_, err := s.BatchUpdate(t.Context(), items)
		return err
	}
	if err := patch(map[string]string{"input_provider": "deepseek", "deepseek_asr_credentials_source": "own"}); err == nil {
		t.Fatal("ASR accepted missing own key")
	}
	if err := patch(map[string]string{"input_provider": "deepseek", "deepseek_asr_credentials_source": "own", "deepseek_asr_api_key": "input-key", "deepseek_tts_enabled": "true", "deepseek_tts_credentials_source": "asr"}); err != nil {
		t.Fatal(err)
	}
	if err := patch(map[string]string{"input_provider": "browser"}); err != nil {
		t.Fatal("disabling input broke reused TTS credentials", err)
	}
	if err := patch(map[string]string{"deepseek_asr_credentials_source": "tts"}); err == nil {
		t.Fatal("cyclic credentials accepted")
	} else {
		var validation *SettingValidationError
		if !errors.As(err, &validation) || validation.Details().Rule != "cyclic_reference" {
			t.Fatal(err)
		}
	}
	if err := patch(map[string]string{"deepseek_tts_credentials_source": "own", "deepseek_tts_api_key": "output-key"}); err != nil {
		t.Fatal(err)
	}
	if err := patch(map[string]string{"input_provider": "deepseek", "deepseek_asr_credentials_source": "tts"}); err != nil {
		t.Fatal("reverse reuse failed", err)
	}
	if _, err := s.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_tts_api_key", Clear: true}}); err == nil {
		t.Fatal("active consumers accepted a cleared key")
	}
}
