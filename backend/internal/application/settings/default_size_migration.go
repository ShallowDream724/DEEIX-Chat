package settings

import (
	"context"
	"strings"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
)

// Only untouched seed rows may move from binary to decimal MB. A saved setting
// remains an explicit choice, even when its value happens to equal an old default.
func (s *Service) migrateUntouchedSizeDefaults(ctx context.Context, existing []domainsettings.SystemSetting) error {
	legacyBytes := map[string]string{
		"storage.user_storage_quota_bytes":          "104857600",
		"storage.max_upload_file_bytes":             "20971520",
		"file.file_full_context_max_bytes":          "2097152",
		"file.image_attachment_processor_max_bytes": "20971520",
		"file.generated_image_max_bytes":            "52428800",
		"file.full_context_max_tokens":              "65536",
	}
	for _, item := range existing {
		if item.CreatedAt.IsZero() || !item.CreatedAt.Equal(item.UpdatedAt) {
			continue
		}
		id := item.Namespace + "." + item.Key
		matches := legacyBytes[id] != "" && strings.TrimSpace(item.Value) == legacyBytes[id]
		if matches {
			if err := s.resetToRegistryDefault(ctx, item.Namespace, item.Key); err != nil {
				return err
			}
		}
	}
	return nil
}
