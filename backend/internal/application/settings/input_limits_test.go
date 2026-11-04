package settings

import (
	"encoding/json"
	"testing"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestInputLimitsSaveValidationAndRuntimeApply(t *testing.T) {
	raw := `{"profiles":{"openai":{"requestBytes":123}},"upstreams":{"7":{"nativeCount":0}}}`
	if err := validatePatchItem(PatchItem{Namespace: "file", Key: "input_limits", Value: raw}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{"profiles":{"unknown":{}}}`, `{"upstreams":{"0":{}}}`, `{"profiles":{"openai":{"imageBytes":-1}}}`} {
		if err := validatePatchItem(PatchItem{Namespace: "file", Key: "input_limits", Value: value}); err == nil {
			t.Fatalf("invalid save accepted: %s", value)
		}
	}
	for _, spec := range settingSpecs {
		if spec.fullKey() == "file:input_limits" {
			cfg := &config.Config{}
			spec.Apply(cfg, raw)
			if cfg.InputLimitsJSON != raw {
				t.Fatalf("runtime did not preserve config: %s", cfg.InputLimitsJSON)
			}
			return
		}
	}
	t.Fatal("input limits setting not registered")
}

func TestInputLimitsResponseMaterializesProfilesWithoutChangingStoredValue(t *testing.T) {
	raw := `{"profiles":{"openai":{"requestBytes":123,"imageCount":null}},"upstreams":{"7":{"nativeCount":0}}}`
	service := &Service{}
	item := domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: raw, ValueType: "json"}
	response := service.settingResponse(item)
	var cfg struct {
		Profiles  map[string]inputlimits.Limits `json:"profiles"`
		Upstreams map[string]map[string]any     `json:"upstreams"`
	}
	if err := json.Unmarshal([]byte(response.Value), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["openai"].RequestBytes != 123 || cfg.Profiles["openai"].ImageCount != 1500 || cfg.Profiles["xai_image_edits"].ImageCount != 3 || cfg.Profiles["xai_video"].ImageCount != 1 || cfg.Profiles["openrouter_images"].ImageCount != 16 {
		t.Fatalf("profile values not materialized: %+v", cfg.Profiles)
	}
	if cfg.Upstreams["7"]["nativeCount"] != float64(0) {
		t.Fatalf("upstream override lost: %+v", cfg.Upstreams)
	}
	if item.Value != raw {
		t.Fatal("stored value changed")
	}
	var defaults struct {
		Profiles map[string]inputlimits.Limits `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(response.DefaultValue), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Profiles["openai"].RequestBytes != 512_000_000 {
		t.Fatal("clearing an override must reveal the true system default, not the saved override")
	}
}
