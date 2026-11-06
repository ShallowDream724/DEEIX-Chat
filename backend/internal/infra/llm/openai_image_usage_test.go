package llm

import (
	"encoding/json"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func imageUsageFixture(t *testing.T, raw string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestImageUsageSeparatesHostedImageBillingFromContext(t *testing.T) {
	parsed := imageUsageFixture(t, `{"status":"completed","usage":{"input_tokens":500,"output_tokens":50},"tools":[{"type":"image_generation","model":"gpt-image-2.6-flare"}],"output":[{"type":"image_generation_call","status":"completed"}],"tool_usage":{"image_gen":{"input_tokens":200,"input_tokens_details":{"text_tokens":80,"image_tokens":120},"output_tokens":400,"cached_tokens":50,"cached_tokens_details":{"cached_text_tokens":20,"cached_image_tokens":30}}}}`)
	usage := parseOpenAICompatibleUsageForAdapter(portllm.AdapterOpenAIResponses, parsed)
	image := usage.ImageToolUsage
	if usage.InputTokens != 500 || usage.OutputTokens != 50 || usage.ImageUsage.Present {
		t.Fatalf("image usage contaminated base totals: %+v", usage)
	}
	if !image.Complete || !image.CacheKnown || image.TextInputTokens != 80 || image.ImageInputTokens != 120 || image.CachedTextInputTokens != 20 || image.CachedImageInputTokens != 30 || image.ImageOutputTokens != 400 || image.Model != "gpt-image-2.6-flare" {
		t.Fatalf("missing image breakdown: %+v", image)
	}
	var audit map[string]any
	if err := json.Unmarshal([]byte(usage.RawUsageJSON), &audit); err != nil || audit["tool_usage"] == nil {
		t.Fatalf("missing audit record: %s", usage.RawUsageJSON)
	}
}

func TestImageUsageUnknownCacheAndOutputBreakdown(t *testing.T) {
	parsed := imageUsageFixture(t, `{"usage":{"input_tokens":120,"input_tokens_details":{"text_tokens":20,"image_tokens":100},"output_tokens":30,"output_tokens_details":{"text_tokens":10,"image_tokens":20}}}`)
	direct, _ := parseOpenAIImageUsage(portllm.AdapterOpenAIImageGenerations, parsed)
	if !direct.Complete || !direct.CacheKnown || direct.ImageOutputTokens != 20 || direct.TextOutputTokens != 10 {
		t.Fatalf("direct usage: %+v", direct)
	}
	parsed["tool_usage"] = map[string]any{"image_gen": parsed["usage"]}
	_, tool := parseOpenAIImageUsage(portllm.AdapterOpenAIResponses, parsed)
	if !tool.Complete || tool.CacheKnown {
		t.Fatalf("omitted hosted cache is unknown: %+v", tool)
	}
	delete(asMap(parsed["usage"])["input_tokens_details"].(map[string]any), "image_tokens")
	broken, _ := parseOpenAIImageUsage(portllm.AdapterOpenAIImageGenerations, parsed)
	if broken.Complete || !broken.Present {
		t.Fatalf("incomplete usage mistaken for precise: %+v", broken)
	}
}

func TestImageUsageMissingFinalRecordDoesNotInventFreeUsage(t *testing.T) {
	parsed := imageUsageFixture(t, `{"status":"completed","output":[{"type":"image_generation_call","status":"completed"}]}`)
	_, image := parseOpenAIImageUsage(portllm.AdapterOpenAIResponses, parsed)
	if !image.Present || image.Complete {
		t.Fatalf("missing usage must remain unknown: %+v", image)
	}
	parsed["status"] = "in_progress"
	_, pending := parseOpenAIImageUsage(portllm.AdapterOpenAIResponses, parsed)
	if pending.Present {
		t.Fatalf("pending event poisoned final usage: %+v", pending)
	}
}
