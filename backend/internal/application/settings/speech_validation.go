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
	requiresKey := effective[settingKey("speech", "input_provider")] == "deepseek" || effective[settingKey("speech", "deepseek_tts_enabled")] == "true"
	if requiresKey && strings.TrimSpace(effective[settingKey("speech", "deepseek_api_key")]) == "" {
		return newSettingValidationError(settingValidationCode("speech", "deepseek_api_key"), SettingValidationDetails{Field: "speech:deepseek_api_key", Rule: "required"})
	}
	return nil
}
