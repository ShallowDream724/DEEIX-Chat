package settings

import (
	"context"
	"encoding/json"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
)

// The unpublished profile/protocol budget shape is intentionally replaced by
// one system default and one flat budget per upstream. Only this setting resets.
func (s *Service) migrateInputLimitsShape(ctx context.Context, existing []domainsettings.SystemSetting) error {
	for _, item := range existing {
		if item.Namespace != "file" || item.Key != "input_limits" {
			continue
		}
		if legacyInputLimitsShape(item.Value) {
			return s.resetToRegistryDefault(ctx, "file", "input_limits")
		}
	}
	return nil
}

func legacyInputLimitsShape(raw string) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return false
	}
	if _, exists := root["profiles"]; exists {
		return true
	}
	var upstreams map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["upstreams"], &upstreams); err != nil {
		return false
	}
	for _, upstream := range upstreams {
		if _, exists := upstream["protocols"]; exists {
			return true
		}
	}
	return false
}
