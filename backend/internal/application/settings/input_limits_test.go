package settings

import (
	"encoding/json"
	"testing"

	domainsettings "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/settings"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestInputLimitsSaveValidationAndRuntimeApply(t *testing.T) {
	raw := `{"defaults":{"requestBytes":123},"upstreams":{"7":{"nativeCount":0}}}`
	if err := validatePatchItem(PatchItem{Namespace: "file", Key: "input_limits", Value: raw}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{"profiles":{"openai":{}}}`, `{"upstreams":{"0":{}}}`, `{"defaults":{"imageBytes":-1}}`, `{"upstreams":{"7":{"protocols":{}}}}`} {
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

func TestInputLimitsResponseMaterializesDefaultsWithoutChangingStoredValue(t *testing.T) {
	raw := `{"defaults":{"requestBytes":123,"imageCount":null},"upstreams":{"7":{"nativeCount":0}}}`
	service := &Service{}
	item := domainsettings.SystemSetting{Namespace: "file", Key: "input_limits", Value: raw, ValueType: "json"}
	response := service.settingResponse(item)
	var cfg struct {
		Defaults  inputlimits.Limits        `json:"defaults"`
		Upstreams map[string]map[string]any `json:"upstreams"`
	}
	if err := json.Unmarshal([]byte(response.Value), &cfg); err != nil {
		t.Fatal(err)
	}
	want := inputlimits.Resolve("", "", "", 0)
	want.RequestBytes = 123
	if cfg.Defaults != want {
		t.Fatalf("default values not materialized: %+v", cfg.Defaults)
	}
	if cfg.Upstreams["7"]["nativeCount"] != float64(0) {
		t.Fatalf("upstream override lost: %+v", cfg.Upstreams)
	}
	if item.Value != raw {
		t.Fatal("stored value changed")
	}
	var defaults struct {
		Defaults inputlimits.Limits `json:"defaults"`
	}
	if err := json.Unmarshal([]byte(response.DefaultValue), &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.Defaults != inputlimits.Resolve("", "", "", 0) {
		t.Fatal("clearing an override must reveal the true system default, not the saved override")
	}
}
