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

func (s *Service) migrateSpeechRoleConnections(ctx context.Context, rows []domainsettings.SystemSetting) error {
	values := map[string]domainsettings.SystemSetting{}
	for _, row := range rows {
		if row.Namespace != "speech" {
			continue
		}
		if strings.HasPrefix(row.Key, "deepseek_asr_") && row.Key != "deepseek_asr_enabled" || strings.HasPrefix(row.Key, "deepseek_tts_") && row.Key != "deepseek_tts_enabled" {
			// A new connection was explicitly configured already; seeding may fill
			// missing defaults, but must never substitute an old credential.
			return nil
		}
		values[row.Key] = row
	}
	legacy := values["deepseek_api_key"]
	if strings.TrimSpace(legacy.Value) == "" {
		return nil
	}
	key, err := s.decryptSettingValue(legacy)
	if err != nil {
		return err
	}
	owner, peer := "asr", "tts"
	if values["input_provider"].Value != "deepseek" && values["deepseek_tts_enabled"].Value == "true" {
		owner, peer = "tts", "asr"
	}
	baseURL := strings.TrimSpace(values["deepseek_base_url"].Value)
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	updates := []domainsettings.SystemSetting{}
	for name, value := range map[string]string{
		"deepseek_" + owner + "_credentials_source": "own",
		"deepseek_" + peer + "_credentials_source":  owner,
		"deepseek_" + owner + "_base_url":           baseURL,
		"deepseek_" + owner + "_api_key":            key,
	} {
		spec, _ := lookupSettingSpec("speech", name)
		item := spec.seedSetting()
		item.Value = value
		updates = append(updates, item)
	}
	encrypted, err := s.encryptSettingsForStorage(updates)
	if err != nil {
		return err
	}
	return s.repo.Upsert(ctx, encrypted)
}

// Align untouched old seed values with the shorter audio retention policy;
// an administrator's explicit setting remains authoritative.
func (s *Service) migrateSpeechCacheRetention(ctx context.Context, rows []domainsettings.SystemSetting) error {
	for _, row := range rows {
		if row.Namespace == "speech" && row.Key == "cache_ttl_hours" && row.Value == "72" && !row.CreatedAt.IsZero() && row.CreatedAt.Equal(row.UpdatedAt) {
			spec, _ := lookupSettingSpec("speech", "cache_ttl_hours")
			return s.repo.Upsert(ctx, []domainsettings.SystemSetting{spec.seedSetting()})
		}
	}
	return nil
}
