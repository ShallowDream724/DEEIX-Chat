package inputlimits

import "testing"

func TestProviderConstraintsRequireOfficialHostAndProtocol(t *testing.T) {
	for _, item := range []struct{ protocol, url string }{
		{"anthropic_messages", "https://api.anthropic.com.proxy.test"},
		{"anthropic_messages", "https://proxy.test"},
		{"openai_responses", "https://api.openai.com.proxy.test"},
		{"openai_responses", "https://api.openai.com@proxy.test"},
		{"openai_chat_completions", "https://proxy.test"},
		{"google_generate_content", "https://proxy.test"},
		{"google_generate_content", "https://generativelanguage.googleapis.com.proxy.test"},
		{"google_generate_content", "https://generativelanguage.googleapis.com@proxy.test"},
	} {
		if got := Provider(item.protocol, item.url, 200_000); got != (ProviderRules{}) {
			t.Errorf("third-party %s %s received hard limits: %+v", item.protocol, item.url, got)
		}
	}
	if got := Provider("anthropic_messages", "https://api.anthropic.com", 200_000); got.ImageCount != 100 || got.ImageMaxDimension != 8000 {
		t.Fatalf("missing official constraints: %+v", got)
	}
	if got := Provider("openai_responses", "https://api.openai.com/v1", 0); got.PDFSingleBytes != 49_999_999 || got.PDFBytes != 50_000_000 {
		t.Fatalf("missing OpenAI constraints: %+v", got)
	}
	if got := Provider("google_generate_content", "https://generativelanguage.googleapis.com/v1beta", 0); got.PDFSingleBytes != 50_000_000 || got.PDFPages != 0 {
		t.Fatalf("Google single PDF constraint changed or legacy page budget leaked: %+v", got)
	}
}
