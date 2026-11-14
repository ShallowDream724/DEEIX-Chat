package llm

import "testing"

func TestDeepSeekResponsesAdapterIdentity(t *testing.T) {
	const protocol = AdapterDeepSeekResponses
	if NormalizeAdapter(" DEEPSEEK_RESPONSES ") != protocol || !IsImplementedAdapter(protocol) || !SupportsStreamingAdapter(protocol) {
		t.Fatal("DeepSeek Responses must retain its distinct streaming adapter identity")
	}
	if DefaultEndpointForAdapter(protocol) != EndpointResponses || OptionPolicyProtocolKey(protocol) != AdapterOpenAIResponses {
		t.Fatal("DeepSeek Responses must reuse the Responses endpoint and option policy")
	}
	if SupportsPreviousResponseID(protocol) || IsImageGenerationAdapter(protocol) || IsImageEditAdapter(protocol) || IsVideoGenerationAdapter(protocol) {
		t.Fatal("DeepSeek Responses must not claim unsupported continuation or media endpoints")
	}
	if !SupportsPreviousResponseID(AdapterOpenAIResponses) {
		t.Fatal("legacy OpenAI Responses continuation must remain supported")
	}
}
