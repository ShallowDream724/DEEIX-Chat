package conversation

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// DeepSeek provider evidence and the actual upstream model are both required.
// A platform display name or arbitrary PDF modality never enables paid Files.
func deepSeekNativeFilesPolicy(route *channel.ResolvedRoute) (bool, bool) {
	if route == nil || strings.ToLower(strings.TrimSpace(route.UpstreamModel)) != "deepseek-flash" {
		return false, false
	}
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages:
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
	if capabilities.NativeFiles == nil {
		return official, false
	}
	setting := capabilities.NativeFiles
	if setting.Enabled != nil && !*setting.Enabled {
		return false, false
	}
	if !official && !strings.EqualFold(setting.Provider, "deepseek") {
		return false, false
	}
	return true, strings.EqualFold(setting.Spreadsheet, "native")
}

func deepSeekNativeDocument(att AttachmentInput, spreadsheet bool) bool {
	switch nativeInputMIME(att) {
	case "application/pdf", "application/msword", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/vnd.ms-powerpoint", "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	case "application/vnd.ms-excel", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return spreadsheet
	}
	return false
}

func restrictDeepSeekToolsForRoute(route *channel.ResolvedRoute, options map[string]any) map[string]any {
	if route != nil && strings.EqualFold(strings.TrimSpace(route.UpstreamModel), "deepseek-flash") {
		return options
	}
	tools := providerToolOptionPayloads(options["tools"])
	if len(tools) == 0 {
		return options
	}
	retained := tools[:0]
	for _, tool := range tools {
		if typ, _ := tool["type"].(string); !strings.HasPrefix(typ, "deepseek_") {
			retained = append(retained, tool)
		}
	}
	result := cloneModelOptionMap(options)
	if len(retained) == 0 {
		delete(result, "tools")
	} else {
		result["tools"] = retained
	}
	return result
}
