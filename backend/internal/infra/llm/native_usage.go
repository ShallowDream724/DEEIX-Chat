package llm

import (
	"encoding/json"
	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"strconv"
	"strings"
)

// Qualify counters using this request's authorized identity, never the transport.
func qualifyNativeToolUsage(output *portllm.GenerateOutput, input portllm.GenerateInput, route portllm.RouteConfig) {
	if output == nil || len(input.NativeToolKeys) == 0 {
		return
	}
	usage := output.ServerSideToolUsage
	modelName := route.UpstreamModel
	if output.ModelVersion != "" {
		modelName = output.ModelVersion
	}
	model := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelName)), "models/")
	major := strings.Split(strings.TrimPrefix(model, "gemini-"), ".")[0]
	major = strings.Split(major, "-")[0]
	version, _ := strconv.Atoi(major)
	if strings.HasPrefix(model, "gemini-") && version >= 3 {
		queryCount := int64(0)
		for _, call := range output.ServerToolCalls {
			if call.ToolName != "google_search" {
				continue
			}
			var payload struct {
				Queries []string `json:"queries"`
			}
			if json.Unmarshal([]byte(call.ArgumentsJSON), &payload) == nil {
				for _, query := range payload.Queries {
					if query != "" {
						queryCount++
					}
				}
			}
		}
		if usage == nil {
			usage = map[string]int64{}
		}
		usage["google_search"] = queryCount
	}
	qualified := map[string]int64{}
	for name, count := range usage {
		if count <= 0 {
			continue
		}
		key := input.NativeToolKeys[name]
		if key == "" {
			for _, allowed := range input.NativeToolKeys {
				if name == allowed {
					key = allowed
					break
				}
			}
		}
		if key != "" {
			qualified[key] += count
		}
	}
	output.ServerSideToolUsage = qualified
}
