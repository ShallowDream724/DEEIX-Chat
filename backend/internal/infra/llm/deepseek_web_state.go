package llm

import (
	"encoding/json"
	"net/url"
	"strings"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/deepseekmodel"
)

type deepSeekWebState struct {
	Protocol string           `json:"protocol"`
	Blocks   []map[string]any `json:"blocks"`
}

func isDeepSeekBaseURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.deepseek.com")
}

func deepSeekStateBlocks(raw, protocol string) []map[string]any {
	if len(raw) == 0 || len(raw) > 8*1024*1024 {
		return nil
	}
	var state deepSeekWebState
	if json.Unmarshal([]byte(raw), &state) != nil || state.Protocol != protocol {
		return nil
	}
	return state.Blocks
}

func appendDeepSeekState(result *portllm.GenerateOutput, protocol string, blocks ...map[string]any) {
	if result == nil || len(blocks) == 0 {
		return
	}
	state := deepSeekWebState{Protocol: protocol, Blocks: deepSeekStateBlocks(result.ProviderStateJSON, protocol)}
	for _, block := range blocks {
		id := getString(block["id"])
		if id == "" {
			id = getString(block["tool_use_id"])
		}
		found := false
		for i, previous := range state.Blocks {
			previousID := getString(previous["id"])
			if previousID == "" {
				previousID = getString(previous["tool_use_id"])
			}
			if id != "" && id == previousID && getString(previous["type"]) == getString(block["type"]) {
				state.Blocks[i] = block
				found = true
				break
			}
		}
		if !found {
			state.Blocks = append(state.Blocks, block)
		}
	}
	raw, err := json.Marshal(state)
	if err == nil && len(raw) <= 8*1024*1024 {
		result.ProviderStateJSON = string(raw)
	}
}

func captureDeepSeekChat(result *portllm.GenerateOutput, message map[string]any) {
	steps := asSlice(message["server_tool_calls"])
	if len(steps) == 0 {
		return
	}
	state := deepSeekWebState{Protocol: portllm.AdapterOpenAIChatCompletions}
	for _, rawStep := range steps {
		step := asMap(rawStep)
		state.Blocks = append(state.Blocks, step)
		for _, rawCall := range asSlice(step["calls"]) {
			call := asMap(rawCall)
			tool := portllm.ToolCall{ToolCallID: getString(call["id"]), ToolType: getString(call["type"]), ToolName: getString(call["type"]), ArgumentsJSON: normalizeJSONString(call["action"]), OutputJSON: normalizeJSONString(call), Status: getString(call["status"]), ErrorJSON: normalizeJSONString(call["error"])}
			appendResponsesServerToolCall(result, tool)
		}
	}
	raw, err := json.Marshal(state)
	if err == nil && len(raw) <= 8*1024*1024 {
		result.ProviderStateJSON = string(raw)
	}
	result.Citations = appendUniqueStrings(result.Citations, parseResponseCitations(message)...)
}

func isDeepSeekNativeBlock(block map[string]any) bool {
	typ := getString(block["type"])
	if typ == "server_tool_use" {
		return strings.HasPrefix(getString(block["name"]), "deepseek_")
	}
	return (typ == "web_search_tool_result" || typ == "web_fetch_tool_result") && getString(block["kind"]) != ""
}

func retainDeepSeekStateForRoute(route portllm.RouteConfig, input portllm.GenerateInput) portllm.GenerateInput {
	allowed := deepseekmodel.IsFlash(route.UpstreamModel) && (portllm.NormalizeAdapter(route.Protocol) == portllm.AdapterDeepSeekResponses || input.NativeFiles || isDeepSeekBaseURL(route.BaseURL))
	for _, key := range input.NativeToolKeys {
		if strings.HasPrefix(key, "deepseek.") {
			allowed = deepseekmodel.IsFlash(route.UpstreamModel)
			break
		}
	}
	if allowed {
		return input
	}
	input.Messages = append([]portllm.Message(nil), input.Messages...)
	for i := range input.Messages {
		input.Messages[i].ProviderStateJSON = ""
	}
	return input
}
