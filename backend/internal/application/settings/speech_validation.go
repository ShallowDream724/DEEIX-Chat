package settings

import (
	"context"
	"strings"
)

func (s *Service) validateSpeechConfiguration(ctx context.Context, patches []PatchItem) error {
	relevant := false
	for _, p := range patches {
		if p.Namespace == "speech" {
			relevant = true
			break
		}
	}
	if !relevant {
		return nil
	}
	effective, err := s.loadEffectiveSettings(ctx, "speech")
	if err != nil {
		return err
	}
	applyPatchesToEffectiveSettings(effective, patches, "speech")
	value := func(key string) string { return effective[settingKey("speech", key)] }
	invalid := func(key, rule string) error {
		return newSettingValidationError(settingValidationCode("speech", key), SettingValidationDetails{Field: "speech:" + key, Rule: rule})
	}
	asrSource, ttsSource := value("deepseek_asr_credentials_source"), value("deepseek_tts_credentials_source")
	if asrSource == "tts" && ttsSource == "asr" {
		return invalid("deepseek_tts_credentials_source", "cyclic_reference")
	}
	legacy := asrSource == "" && ttsSource == "" && value("deepseek_asr_api_key") == "" && value("deepseek_tts_api_key") == ""
	ttsProvider := value("tts_provider")
	if ttsProvider == "" {
		ttsProvider = "deepseek"
	}
	if value("deepseek_tts_enabled") == "true" && ttsProvider == "mimo" && strings.TrimSpace(value("mimo_api_key")) == "" {
		return invalid("mimo_api_key", "required")
	}
	for _, role := range []string{"asr", "tts"} {
		enabled := role == "asr" && value("input_provider") == "deepseek" || role == "tts" && value("deepseek_tts_enabled") == "true" && ttsProvider == "deepseek"
		if !enabled {
			continue
		}
		key := "deepseek_" + role + "_api_key"
		if role == "asr" && asrSource == "tts" {
			key = "deepseek_tts_api_key"
		} else if role == "tts" && ttsSource == "asr" {
			key = "deepseek_asr_api_key"
		}
		if legacy {
			key = "deepseek_api_key"
		}
		if strings.TrimSpace(value(key)) == "" {
			return invalid(key, "required")
		}
	}
	return nil
}
