package settings

import (
	"testing"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestSpeechSettingsRejectInvalidPricesLimitsAndEndpoints(t *testing.T) {
	for _, key := range []string{"asr_cny_per_hour", "tts_cny_per_10000_characters"} {
		for _, value := range []string{"NaN", "Inf", "-Inf", "-0.1", "1000001", "", "1e999"} {
			if err := validatePatchItem(PatchItem{Namespace: "speech", Key: key, Value: value}); err == nil {
				t.Errorf("%s accepted invalid price %q", key, value)
			}
		}
		for _, value := range []string{"0", "0.39", "1.5", "1000000"} {
			if err := validatePatchItem(PatchItem{Namespace: "speech", Key: key, Value: value}); err != nil {
				t.Errorf("%s rejected valid price %q: %v", key, value, err)
			}
		}
	}
	for key, values := range map[string][]string{
		"max_recording_seconds":   {"9", "1801"},
		"max_concurrent_per_user": {"0", "9"},
		"deepseek_base_url":       {"", "ftp://example.com", "https://user:secret@example.com", "https://example.com?key=secret", "http://169.254.169.254/latest/meta-data"},
	} {
		for _, value := range values {
			if err := validatePatchItem(PatchItem{Namespace: "speech", Key: key, Value: value}); err == nil {
				t.Errorf("%s accepted invalid value %q", key, value)
			}
		}
	}
}

func TestSpeechSettingsApplyRuntimeSnapshot(t *testing.T) {
	var runtime RuntimeSettings
	var cfg config.Config
	for _, spec := range speechSettingSpecs() {
		runtime.applyItem(&cfg, spec.seedSetting())
	}
	if cfg.Speech.DeepSeekBaseURL != "https://api.deepseek.com" || cfg.Speech.MaxRecordingSeconds != 120 || cfg.Speech.MaxConcurrentPerUser != 2 || cfg.Speech.ASRCNYPerHour != 0.39 || cfg.Speech.TTSCNYPer10000Characters != 1.5 {
		t.Fatalf("speech defaults not applied: %+v", cfg.Speech)
	}
	runtime.applyItem(&cfg, domainsettings.SystemSetting{Namespace: "speech", Key: "max_recording_seconds", Value: "300"})
	if cfg.Speech.MaxRecordingSeconds != 300 {
		t.Fatal("runtime speech setting did not update")
	}
}

func TestSpeechAPIKeyIsEncryptedMaskedAndPreservedOnBlankSave(t *testing.T) {
	service, repo, _ := newNativeToolPricingTestService()
	const credential = "test-speech-credential"
	if _, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_api_key", Value: credential}}); err != nil {
		t.Fatal(err)
	}
	stored := repo.byNamespace["speech"][0]
	if stored.Value == credential || stored.Value == "" {
		t.Fatal("speech key was not encrypted")
	}
	masked := service.settingResponse(stored)
	if masked.Value != "" || !masked.Sensitive || !masked.Configured {
		t.Fatalf("speech key not masked: %+v", masked)
	}
	runtime := config.NewRuntime(config.Config{})
	settingsRuntime := NewRuntimeSettings(repo, nil, testKeyring())
	if err := settingsRuntime.ApplyTo(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}
	previous := runtime.Snapshot()
	if previous.Speech.DeepSeekAPIKey != credential || previous.Speech.TTSEnabled {
		t.Fatal("runtime snapshot did not decrypt the saved speech credential")
	}
	if _, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_api_key", Value: ""}, {Namespace: "speech", Key: "deepseek_tts_enabled", Value: "true"}}); err != nil {
		t.Fatal(err)
	}
	values, err := service.RuntimeValuesByNamespace(t.Context(), "speech")
	if err != nil || values["deepseek_api_key"] != credential || values["deepseek_tts_enabled"] != "true" {
		t.Fatal("blank save did not preserve runtime credential and enabled setting")
	}
	if err := settingsRuntime.ApplyTo(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.Snapshot().Speech.TTSEnabled || previous.Speech.TTSEnabled {
		t.Fatal("speech update did not produce an independent runtime snapshot")
	}
	if _, err := service.BatchUpdate(t.Context(), []PatchItem{{Namespace: "speech", Key: "deepseek_api_key", Clear: true}, {Namespace: "speech", Key: "deepseek_tts_enabled", Value: "false"}}); err != nil {
		t.Fatal(err)
	}
	values, err = service.RuntimeValuesByNamespace(t.Context(), "speech")
	if err != nil || values["deepseek_api_key"] != "" {
		t.Fatal("explicit clear did not remove speech credential")
	}
}
