package settings

import (
	"context"
	"encoding/json"
	"strconv"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

// Move the old attachment count into the single budget editor without losing
// an administrator's saved count or explicit unlimited media overrides.
func (s *Service) migrateInputBudgetDefaults(ctx context.Context, existing []domainsettings.SystemSetting) error {
	count := 10
	var previous *domainsettings.SystemSetting
	for i := range existing {
		item := &existing[i]
		if item.Namespace == "storage" && item.Key == "max_message_files" {
			if value, err := strconv.Atoi(item.Value); err == nil && value > 0 {
				count = value
			}
		}
		if item.Namespace == "file" && item.Key == "input_limits" {
			previous = item
		}
	}
	if count == 10 && (previous == nil || legacyInputLimitsShape(previous.Value)) {
		return nil
	}
	raw := inputlimits.DefaultConfigJSON()
	if previous != nil && !legacyInputLimitsShape(previous.Value) {
		if inputlimits.Validate(previous.Value) != nil {
			return nil
		}
		raw = previous.Value
		if !previous.CreatedAt.IsZero() && previous.CreatedAt.Equal(previous.UpdatedAt) && untouchedInputBudgetDefaults(raw) {
			raw = inputlimits.DefaultConfigJSON()
		}
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return err
	}
	var defaults map[string]any
	if err := json.Unmarshal(root["defaults"], &defaults); err != nil || defaults == nil {
		defaults = make(map[string]any)
	}
	if previous != nil && raw == previous.Value {
		if _, exists := defaults["messageFileCount"]; exists {
			return nil
		}
	} else if count == 10 && previous == nil {
		return nil
	}
	defaults["messageFileCount"] = count
	root["defaults"], _ = json.Marshal(defaults)
	updated, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return s.repo.Upsert(ctx, []domainsettings.SystemSetting{{Namespace: "file", Key: "input_limits", Value: string(updated), ValueType: "json"}})
}

func untouchedInputBudgetDefaults(raw string) bool {
	var value struct {
		Defaults  map[string]int64           `json:"defaults"`
		Upstreams map[string]json.RawMessage `json:"upstreams"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil || len(value.Defaults) != 10 || len(value.Upstreams) != 0 || value.Defaults["requestBytes"] != 100_000_000 {
		return false
	}
	for key, number := range value.Defaults {
		if key != "requestBytes" && number != 0 {
			return false
		}
	}
	return true
}
