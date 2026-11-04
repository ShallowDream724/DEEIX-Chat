package inputlimits

import (
	"encoding/json"
	"testing"
)

func TestResolveDefaultsAndFlatUpstreamOverrides(t *testing.T) {
	raw := `{"defaults":{"imageCount":4,"nativeBytes":null,"pdfPages":55},"upstreams":{"7":{"imageCount":2,"imageBytes":99,"pdfPages":null},"8":{"requestBytes":0}}}`
	if err := Validate(raw); err != nil {
		t.Fatal(err)
	}
	want := defaults()
	want.ImageCount, want.ImageBytes, want.PDFPages = 2, 99, 55
	for _, route := range []struct{ protocol, baseURL string }{
		{"openai_responses", "https://api.openai.com/v1"},
		{"openai_chat_completions", "https://proxy.example/v1"},
		{"anthropic_messages", "https://api.anthropic.com"},
		{"google_generate_content", "https://generativelanguage.googleapis.com"},
		{"xai_image_edits", "https://api.x.ai/v1"},
		{"xai_video", "https://api.x.ai/v1"},
		{"openrouter_images", "https://openrouter.ai"},
		{"gemini_interactions", ""},
	} {
		if got := Resolve(raw, route.protocol, route.baseURL, 7); got != want {
			t.Fatalf("same upstream received protocol-specific budget for %s: %+v", route.protocol, got)
		}
		if got := Resolve("", route.protocol, route.baseURL, 0); got != defaults() {
			t.Fatalf("hidden default budget for %s: %+v", route.protocol, got)
		}
	}
	if got := Resolve(raw, "", "", 8); got.RequestBytes != 0 || got.ImageCount != 4 {
		t.Fatalf("zero override or defaults lost: %+v", got)
	}
	if got := Resolve(raw, "", "", 9); got.ImageCount != 4 || got.ImageBytes != defaults().ImageBytes {
		t.Fatalf("upstream override leaked to another upstream: %+v", got)
	}
}

func TestMaterializeDefaultsPreservesSparseUpstreamOverrides(t *testing.T) {
	raw := `{"defaults":{"requestBytes":123,"imageCount":null},"upstreams":{"7":{"requestBytes":0,"imageCount":null}}}`
	var materialized struct {
		Defaults  Limits                    `json:"defaults"`
		Upstreams map[string]map[string]any `json:"upstreams"`
	}
	if err := json.Unmarshal([]byte(Materialize(raw)), &materialized); err != nil {
		t.Fatal(err)
	}
	want := defaults()
	want.RequestBytes = 123
	if materialized.Defaults != want {
		t.Fatalf("wrong effective defaults: %+v", materialized.Defaults)
	}
	if len(materialized.Upstreams["7"]) != 1 || materialized.Upstreams["7"]["requestBytes"] != float64(0) {
		t.Fatalf("upstream inheritance was expanded or lost: %+v", materialized.Upstreams)
	}
}

func TestValidateInputLimits(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `[]`, `{`, `{} {}`, `{"unknown":{}}`, `{"Defaults":{}}`, `{"defaults":{"RequestBytes":1}}`,
		`{"profiles":{"openai":{}}}`, `{"defaults":{"unknown":1}}`, `{"defaults":null}`, `{"upstreams":null}`,
		`{"defaults":{"requestBytes":-1}}`, `{"defaults":{"imageCount":1.5}}`, `{"defaults":{"requestBytes":9007199254740992}}`,
		`{"defaults":{"imageCount":2147483648}}`, `{"upstreams":{"1":{"protocols":{"openai_responses":{"imageCount":1}}}}}`,
		`{"upstreams":{"0":{}}}`, `{"upstreams":{"-1":{}}}`, `{"upstreams":{"01":{}}}`, `{"upstreams":{"x":{}}}`, `{"upstreams":{"1":null}}`,
	} {
		if err := Validate(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"defaults":{"imageCount":0,"requestBytes":null}}`, `{"upstreams":{"1":{"nativeBytes":9007199254740991}}}`, DefaultConfigJSON()} {
		if err := Validate(raw); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestDefaultConfigContainsOneCompleteBudget(t *testing.T) {
	if got := Materialize(`{"invalid":true}`); got != `{"invalid":true}` {
		t.Fatalf("invalid stored configuration was hidden: %s", got)
	}
	var cfg map[string]map[string]any
	if err := json.Unmarshal([]byte(DefaultConfigJSON()), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 2 || len(cfg["defaults"]) != 11 || cfg["upstreams"] == nil || len(cfg["upstreams"]) != 0 {
		t.Fatalf("unexpected configuration shape: %+v", cfg)
	}
	for key, value := range cfg["defaults"] {
		want := map[string]float64{"messageFileCount": 10, "requestBytes": 100_000_000, "imageCount": 20, "imageSingleBytes": 5_000_000, "imageBytes": 20_000_000, "nativeCount": 10, "nativeBytes": 50_000_000, "pdfSingleBytes": 50_000_000, "pdfBytes": 50_000_000, "pdfPages": 200, "nonPDFBytes": 50_000_000}[key]
		if value != want {
			t.Errorf("default %s = %v, want %v", key, value, want)
		}
	}
}
