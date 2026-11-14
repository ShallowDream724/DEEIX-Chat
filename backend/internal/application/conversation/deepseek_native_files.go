package conversation

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/deepseekfiles"
)

// DeepSeek provider evidence and the actual upstream model are both required.
// A platform display name or arbitrary PDF modality never enables paid Files.
func deepSeekNativeFilesPolicy(route *channel.ResolvedRoute) (bool, bool) {
	if route == nil || !deepseekfiles.SupportsModel(route.UpstreamModel) {
		return false, false
	}
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterDeepSeekResponses, llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages:
	default:
		return false, false
	}
	var capabilities struct {
		NativeFiles *struct {
			Provider    string `json:"provider"`
			Enabled     *bool  `json:"enabled"`
			Spreadsheet string `json:"spreadsheet"`
		} `json:"nativeFiles"`
	}
	_ = json.Unmarshal([]byte(route.ModelCapabilitiesJSON), &capabilities)
	parsed, _ := url.Parse(route.BaseURL)
	official := parsed != nil && strings.EqualFold(parsed.Hostname(), "api.deepseek.com")
	explicit := llm.NormalizeAdapter(route.Protocol) == llm.AdapterDeepSeekResponses
	if capabilities.NativeFiles == nil {
		return explicit || official, false
	}
	setting := capabilities.NativeFiles
	if setting.Enabled != nil && !*setting.Enabled {
		return false, false
	}
	if !explicit && !official && !strings.EqualFold(setting.Provider, "deepseek") {
		return false, false
	}
	return true, strings.EqualFold(setting.Spreadsheet, "native")
}

func deepSeekNativeDocument(att AttachmentInput, spreadsheet bool) bool {
	format, ok := deepseekfiles.Resolve(att.FileName, nativeInputMIME(att))
	return ok && (format.Kind == deepseekfiles.Document || (format.Kind == deepseekfiles.Spreadsheet && spreadsheet))
}
