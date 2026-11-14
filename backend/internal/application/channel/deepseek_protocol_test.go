package channel

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestDeepSeekResponsesRouteAndExternalModelProtocols(t *testing.T) {
	const protocol = llm.AdapterDeepSeekResponses
	got, err := resolveRouteProtocol(protocol, compatibleOpenAI, "", `["chat"]`)
	if err != nil || got != protocol {
		t.Fatalf("explicit DeepSeek route = %q, %v", got, err)
	}
	if !IsRouteAllowedForTask(TaskTypeChat, `["chat"]`, protocol) || !isLightweightModelProbeProtocol(protocol) {
		t.Fatal("DeepSeek Responses must support chat and model probes")
	}
	if IsRouteAllowedForTask(TaskTypeImageGeneration, `["image_gen"]`, protocol) {
		t.Fatal("DeepSeek Responses must not claim image generation")
	}
	protocols, ok := NormalizeExternalModelProtocols([]string{" DEEPSEEK_RESPONSES "})
	if !ok || len(protocols) != 1 || protocols[0] != protocol {
		t.Fatalf("external protocol = %v, %v", protocols, ok)
	}
	for _, model := range []string{"deepseek-flash", "deepseek-pro"} {
		protocols := SuggestExternalModelProtocols(protocol, model)
		if len(protocols) != 1 || protocols[0] != protocol || externalRouteProtocol(model, protocols, TaskTypeChat) != protocol {
			t.Fatalf("%s must retain the DeepSeek Responses text route: %v", model, protocols)
		}
	}
}
