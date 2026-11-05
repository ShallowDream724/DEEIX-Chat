package llm

import "testing"

func TestChat01AdapterIdentity(t *testing.T) {
	const protocol = AdapterChat01ChatCompletions
	if !IsImplementedAdapter(protocol) || !SupportsStreamingAdapter(protocol) || DefaultEndpointForAdapter(protocol) != EndpointChatCompletions || OptionPolicyProtocolKey(protocol) != protocol {
		t.Fatal("Chat01 must be a distinct streaming Chat Completions adapter")
	}
	if SupportsPreviousResponseID(protocol) || IsImageGenerationAdapter(protocol) || IsImageEditAdapter(protocol) || IsVideoGenerationAdapter(protocol) {
		t.Fatal("Chat01 must not claim unrelated endpoint capabilities")
	}
}
