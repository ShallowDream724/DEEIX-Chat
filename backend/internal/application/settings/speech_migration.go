package settings

import (
	"context"
	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"strings"
)

// A legacy list of enabled backends did not express an administrator's single
// provider choice. Only an explicit new input_provider is authoritative; all
// other deployments start on the required browser default.
func (s *Service) migrateSpeechInputProvider(ctx context.Context, rows []domainsettings.SystemSetting) error {
	values := map[string]string{}
	for _, r := range rows {
		if r.Namespace == "speech" {
			values[r.Key] = strings.TrimSpace(r.Value)
		}
	}
	provider := values["input_provider"]
	if provider != "browser" && provider != "doubao-ime" && provider != "wetype" && provider != "deepseek" {
		spec, _ := lookupSettingSpec("speech", "input_provider")
		updates := []domainsettings.SystemSetting{spec.seedSetting()}
		if values["enabled"] != "true" && values["deepseek_tts_enabled"] == "true" {
			spec, _ := lookupSettingSpec("speech", "deepseek_tts_enabled")
			updates = append(updates, spec.seedSetting())
		}
		if err := s.repo.Upsert(ctx, updates); err != nil {
			return err
		}
	}
	for _, key := range []string{"enabled", "doubao_enabled", "wetype_enabled", "deepseek_asr_enabled", "input_provider_migration_required"} {
		if err := s.repo.Delete(ctx, "speech", key); err != nil {
			return err
		}
	}
	return nil
}
