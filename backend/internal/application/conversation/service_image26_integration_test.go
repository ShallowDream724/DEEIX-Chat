package conversation

import (
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/imageusage"
)

func TestImageToolModelRemainsAdministratorControlled(t *testing.T) {
	for _, capabilities := range []string{
		`{"nativeTools":[{"key":"openai.image_generation","protocols":["openai_responses"],"payload":{"type":"image_generation","model":"gpt-image-2.6-flare","output_format":"gif"}}]}`,
		`{"defaultOptions":{"tools":[{"type":"image_generation","model":"gpt-image-2.6-flare","output_format":"gif"}]}}`,
	} {
		filtered := filterModelOptions(map[string]any{"tools": []any{map[string]any{"type": "image_generation", "model": "forbidden-model", "headers": map[string]any{"x": "bad"}}}}, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{Mode: modelOptionPolicyAllowlist, AllowedPathsJSON: config.DefaultModelOptionAllowedPathsJSON(), DeniedPathsJSON: config.DefaultModelOptionDeniedPathsJSON(), ModelCapabilitiesJSON: capabilities})
		tools := providerToolOptionPayloads(filtered["tools"])
		if len(tools) != 1 || tools[0]["model"] != "gpt-image-2.6-flare" || tools[0]["headers"] != nil {
			t.Fatalf("wrong image permission: %#v", tools)
		}
		if model, ok := selectedNativeImageModel(llm.AdapterOpenAIResponses, filtered, capabilities); !ok || model != "gpt-image-2.6-flare" {
			t.Fatalf("billing and dispatch disagree: %q %v", model, ok)
		}
	}
	filtered := filterModelOptions(map[string]any{"tools": []any{map[string]any{"type": "image_generation", "model": "forbidden-model"}}}, llm.AdapterOpenAIResponses, modelOptionPolicyConfig{Mode: modelOptionPolicyAllowlist, AllowedPathsJSON: config.DefaultModelOptionAllowedPathsJSON(), ModelCapabilitiesJSON: `{"nativeToolKeys":["openai.image_generation"]}`})
	if tools := providerToolOptionPayloads(filtered["tools"]); len(tools) != 1 || tools[0]["model"] != nil {
		t.Fatalf("caller chose undeclared nested model: %#v", tools)
	}
}

func TestImageToolUsageCumulativeEventsAndFollowUp(t *testing.T) {
	observed := llm.Usage{InputTokens: 10, ImageToolUsage: imageusage.Usage{Present: true, Complete: true, ImageOutputTokens: 400}}
	total := addLLMUsage(llm.Usage{}, diffLLMUsage(observed, llm.Usage{}))
	total = addLLMUsage(total, diffLLMUsage(observed, observed))
	if total.ImageToolUsage.ImageOutputTokens != 400 {
		t.Fatalf("duplicate terminal charged twice: %+v", total)
	}
	total = addLLMUsage(total, observed)
	if total.ImageToolUsage.ImageOutputTokens != 800 || total.InputTokens != 20 {
		t.Fatalf("new call not billed: %+v", total)
	}
}

func TestGeneratedDeliverableDoesNotBecomeSkippedInputNotice(t *testing.T) {
	old := AttachmentInput{FileID: "generated", FileName: "result.gif", MessageRole: "assistant", ContextMode: fileContextModeSkipped}
	summary, _, payload := buildAttachmentProcessTrace("full", []AttachmentInput{old})
	if summary != "" || payload != nil {
		t.Fatalf("generated output caused input warning: %s", summary)
	}
	old.Current, old.MessageRole = true, "user"
	summary, _, payload = buildAttachmentProcessTrace("full", []AttachmentInput{old})
	if !strings.Contains(summary, "未纳入 1") || payload == nil {
		t.Fatalf("explicit input warning hidden: %s", summary)
	}
}
