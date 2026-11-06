package llm

import (
	"encoding/json"
	"math"
	"strings"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/imageusage"
)

// Image token details are independent of the text-model counters. Never add
// hosted image tokens to Responses input/output totals used for context sizing.
func parseOpenAIImageUsage(adapter string, response map[string]any) (imageusage.Usage, imageusage.Usage) {
	switch portllm.NormalizeAdapter(adapter) {
	case portllm.AdapterOpenAIImageGenerations, portllm.AdapterOpenAIImageEdits:
		return parseImageTokenBreakdown(asMap(response["usage"]), strings.TrimSpace(getString(response["model"])), true), imageusage.Usage{}
	case portllm.AdapterOpenAIResponses:
		toolUsage := asMap(asMap(response["tool_usage"])["image_gen"])
		model := ""
		for _, raw := range asSlice(response["tools"]) {
			tool := asMap(raw)
			if getString(tool["type"]) == "image_generation" {
				model = strings.TrimSpace(getString(tool["model"]))
			}
		}
		called, completedImage := false, false
		for _, raw := range asSlice(response["output"]) {
			item := asMap(raw)
			if getString(item["type"]) == "image_generation_call" {
				called = true
				completedImage = completedImage || getString(item["status"]) == "completed"
			}
		}
		usage := parseImageTokenBreakdown(toolUsage, model, false)
		// The API may skip image calls with unavailable usage when building its
		// aggregate. A completed image paired with all-zero counters is not proof
		// of a free generation.
		if completedImage && usage.TextInputTokens+usage.ImageInputTokens+usage.ImageOutputTokens+usage.TextOutputTokens == 0 {
			usage.Complete = false
		}
		if !called && getInt64FromPath(toolUsage, "total_tokens") == 0 && getInt64FromPath(toolUsage, "output_tokens") == 0 && getInt64FromPath(toolUsage, "input_tokens") == 0 {
			return imageusage.Usage{}, imageusage.Usage{}
		}
		// Missing terminal usage is auditable; in-progress tool items alone do
		// not invalidate the breakdown that will arrive with response.completed.
		status := getString(response["status"])
		if called && !usage.Present && (status == "completed" || status == "incomplete" || status == "failed") {
			usage.Present = true
		}
		return imageusage.Usage{}, usage
	default:
		return imageusage.Usage{}, imageusage.Usage{}
	}
}

func parseImageTokenBreakdown(raw map[string]any, model string, direct bool) imageusage.Usage {
	if len(raw) == 0 {
		return imageusage.Usage{}
	}
	result := imageusage.Usage{Model: model, Present: true, CacheKnown: direct}
	details := asMap(raw["input_tokens_details"])
	input, inputOK := imageTokenCount(raw, "input_tokens")
	text, textOK := imageTokenCount(details, "text_tokens")
	image, imageOK := imageTokenCount(details, "image_tokens")
	output, outputOK := imageTokenCount(raw, "output_tokens")
	result.TextInputTokens, result.ImageInputTokens, result.ImageOutputTokens = text, image, output
	result.Complete = inputOK && textOK && imageOK && outputOK && text <= input && image == input-text
	if outputDetails := asMap(raw["output_tokens_details"]); len(outputDetails) != 0 {
		var imageOutputOK, textOutputOK bool
		result.ImageOutputTokens, imageOutputOK = imageTokenCount(outputDetails, "image_tokens")
		result.TextOutputTokens, textOutputOK = imageTokenCount(outputDetails, "text_tokens")
		result.Complete = result.Complete && imageOutputOK && textOutputOK && result.TextOutputTokens <= output && result.ImageOutputTokens == output-result.TextOutputTokens
	}
	cacheDetails := asMap(raw["cached_tokens_details"])
	if len(cacheDetails) > 0 {
		var textCacheOK, imageCacheOK bool
		result.CachedTextInputTokens, textCacheOK = imageTokenCount(cacheDetails, "cached_text_tokens")
		result.CachedImageInputTokens, imageCacheOK = imageTokenCount(cacheDetails, "cached_image_tokens")
		result.CacheKnown = textCacheOK && imageCacheOK
		result.Complete = result.Complete && result.CacheKnown && result.CachedTextInputTokens <= text && result.CachedImageInputTokens <= image
		if cached, ok := imageTokenCount(raw, "cached_tokens"); ok {
			result.Complete = result.Complete && result.CachedTextInputTokens <= cached && result.CachedImageInputTokens == cached-result.CachedTextInputTokens
		}
	} else if cached, ok := imageTokenCount(raw, "cached_tokens"); ok {
		// An aggregate cache count cannot identify two differently priced inputs.
		result.CacheKnown = cached == 0
		if cached > 0 {
			result.Complete = false
		}
	}
	return result
}

func imageTokenCount(raw map[string]any, key string) (int64, bool) {
	value, exists := raw[key]
	if !exists {
		return 0, false
	}
	switch n := value.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= math.MaxInt64 || math.Trunc(n) != n {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, n >= 0
	case int:
		return int64(n), n >= 0
	case json.Number:
		count, err := n.Int64()
		return count, err == nil && count >= 0
	default:
		return 0, false
	}
}

func openAIUsageAuditJSON(parsed map[string]any) string {
	raw := cloneMap(asMap(parsed["usage"]))
	if raw == nil {
		raw = make(map[string]any)
	}
	if tools := asMap(parsed["tool_usage"]); len(tools) > 0 {
		raw["tool_usage"] = tools
	}
	if len(raw) == 0 {
		return ""
	}
	return normalizeJSONString(raw)
}
