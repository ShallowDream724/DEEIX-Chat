package inputlimits

import (
	"encoding/json"
	"testing"
)

func TestResolveProfilesAndLayeredOverrides(t *testing.T) {
	raw := `{"profiles":{"openai":{"imageCount":4,"nativeBytes":null,"pdfBytes":0},"compatible":{"requestBytes":123}},"upstreams":{"7":{"imageCount":2,"imageBytes":99}}}`
	if err := Validate(raw); err != nil {
		t.Fatal(err)
	}
	got := Resolve(raw, "openai_responses", "https://api.openai.com/v1", 7)
	if got.ImageCount != 2 || got.ImageBytes != 99 || got.PDFBytes != 0 || got.NativeBytes != 50_000_000 {
		t.Fatalf("bad merge: %+v", got)
	}
	if got := Resolve(raw, "openai_chat_completions", "https://proxy.example/v1", 0); got.RequestBytes != 123 {
		t.Fatalf("compatible host received official profile: %+v", got)
	}
	if got := Resolve("", "openai_responses", "https://api.openai.com.evil.test", 0); got.RequestBytes != 100_000_000 {
		t.Fatalf("suffix host received official profile: %+v", got)
	}
	if got := Resolve("", "google_generate_content", "https://proxy.example", 0); got.PDFSingleBytes != 50_000_000 || got.PDFPages != 200 {
		t.Fatalf("google default: %+v", got)
	}
	if got := Resolve("", "gemini_interactions", "", 0); got.NativeBytes != 0 || got.ImageCount != 0 {
		t.Fatalf("interactions default: %+v", got)
	}
	if got := Resolve("", "anthropic_messages", "https://api.anthropic.com", 0); got.RequestBytes != 128_000_000 || got.ImageCount != 600 {
		t.Fatalf("anthropic default: %+v", got)
	}
	if got := Resolve("", "openrouter_responses", "", 0); got.NativeCount != 0 || got.ImageCount != 0 {
		t.Fatalf("openrouter default: %+v", got)
	}
}

func TestUpstreamProtocolOverridesPreserveFlatConfiguration(t *testing.T) {
	raw := `{"upstreams":{"7":{"requestBytes":123,"protocols":{"openai_responses":{"imageCount":2},"anthropic_messages":{"requestBytes":456}}}}}`
	if err := Validate(raw); err != nil {
		t.Fatal(err)
	}
	responses := Resolve(raw, "openai_responses", "https://proxy.test", 7)
	if responses.RequestBytes != 123 || responses.ImageCount != 2 {
		t.Fatalf("responses: %+v", responses)
	}
	anthropic := Resolve(raw, "anthropic_messages", "https://proxy.test", 7)
	if anthropic.RequestBytes != 456 || anthropic.ImageCount != 0 {
		t.Fatalf("anthropic: %+v", anthropic)
	}
	var materialized map[string]json.RawMessage
	if err := json.Unmarshal([]byte(Materialize(raw)), &materialized); err != nil {
		t.Fatal(err)
	}
	var upstreams map[string]map[string]json.RawMessage
	_ = json.Unmarshal(materialized["upstreams"], &upstreams)
	if len(upstreams["7"]) != 2 || upstreams["7"]["protocols"] == nil {
		t.Fatalf("upstream overrides were expanded or lost: %s", materialized["upstreams"])
	}
	for _, invalid := range []string{
		`{"upstreams":{"1":{"protocols":{"unknown":{}}}}}`,
		`{"upstreams":{"1":{"protocols":{"OPENAI_RESPONSES":{}}}}}`,
		`{"upstreams":{"1":{"protocols":{"openai_responses":{"unknown":1}}}}}`,
		`{"upstreams":{"1":{"protocols":{"openai_responses":{"imageCount":-1}}}}}`,
		`{"upstreams":{"1":{"protocols":{"openai_responses":null}}}}`,
	} {
		if err := Validate(invalid); err == nil {
			t.Fatalf("accepted invalid protocol override: %s", invalid)
		}
	}
}

func TestValidateInputLimits(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `[]`, `{`, `{} {}`, `{"unknown":{}}`, `{"Profiles":{}}`, `{"profiles":{"openai":{"RequestBytes":1}}}`,
		`{"profiles":{"unknown":{}}}`, `{"profiles":{"openai":{"unknown":1}}}`,
		`{"profiles":{"openai":null}}`, `{"profiles":{"openai":{"requestBytes":-1}}}`,
		`{"profiles":{"openai":{"imageCount":1.5}}}`, `{"profiles":{"openai":{"requestBytes":9007199254740992}}}`,
		`{"profiles":{"openai":{"imageCount":2147483648}}}`,
		`{"upstreams":{"0":{}}}`, `{"upstreams":{"-1":{}}}`, `{"upstreams":{"01":{}}}`, `{"upstreams":{"x":{}}}`,
	} {
		if err := Validate(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"profiles":{"openai":{"imageCount":0,"requestBytes":null}}}`, `{"upstreams":{"1":{"nativeBytes":9007199254740991}}}`, DefaultConfigJSON()} {
		if err := Validate(raw); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestDefaultConfigContainsEveryCompleteProfile(t *testing.T) {
	if got := Materialize(`{"invalid":true}`); got != `{"invalid":true}` {
		t.Fatalf("invalid stored configuration was hidden: %s", got)
	}
	var cfg struct {
		Profiles  map[string]map[string]any `json:"profiles"`
		Upstreams map[string]any            `json:"upstreams"`
	}
	if err := json.Unmarshal([]byte(DefaultConfigJSON()), &cfg); err != nil {
		t.Fatal(err)
	}
	expectedProfiles := []string{"openai", "google_generate_content", "gemini_interactions", "anthropic", "openrouter", "compatible", "xai_image_edits", "xai_video", "openrouter_images"}
	if len(cfg.Profiles) != len(expectedProfiles) || cfg.Upstreams == nil || len(cfg.Upstreams) != 0 {
		t.Fatalf("incomplete defaults: %+v", cfg)
	}
	for _, profile := range expectedProfiles {
		if _, ok := cfg.Profiles[profile]; !ok {
			t.Errorf("missing profile %s", profile)
		}
	}
	for profile, values := range cfg.Profiles {
		if len(values) != 10 {
			t.Errorf("%s has %d fields", profile, len(values))
		}
	}
}

func TestMediaTaskDefaultsAndProtocolOverride(t *testing.T) {
	for protocol, count := range map[string]int{"xai_image_edits": 3, "xai_video": 1, "openrouter_images": 16} {
		if got := Resolve("", protocol, "https://compatible.example/v1", 0); got.ImageCount != count {
			t.Fatalf("%s image count=%d", protocol, got.ImageCount)
		}
	}
	got := Resolve(`{"upstreams":{"7":{"protocols":{"xai_image_edits":{"imageCount":2},"xai_video":{"imageCount":0}}}}}`, "xai_image_edits", "https://api.x.ai/v1", 7)
	if got.ImageCount != 2 {
		t.Fatalf("edit override ignored: %+v", got)
	}
	got = Resolve(`{"upstreams":{"7":{"protocols":{"xai_video":{"imageCount":0}}}}}`, "xai_video", "https://api.x.ai/v1", 7)
	if got.ImageCount != 0 {
		t.Fatalf("explicit operational zero ignored: %+v", got)
	}
}
