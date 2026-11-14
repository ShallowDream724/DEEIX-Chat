package nativetool

import (
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/deepseekmodel"
)

// DefinitionForModel selects a model's wire contract without changing its
// administrator-facing capability or billing identity. An empty model is used
// by catalog/configuration callers; dispatch must supply the upstream model.
func DefinitionForModel(definition Definition, model string) (Definition, bool) {
	if !strings.HasPrefix(definition.Key, "deepseek.") || strings.TrimSpace(model) == "" {
		return definition, true
	}
	switch {
	case deepseekmodel.IsFlash(model):
		return definition, true
	case deepseekmodel.IsPro(model):
		if definition.Key != "deepseek.tool_search" || (definition.Protocol != "deepseek_responses" && definition.Protocol != "openai_responses") {
			return Definition{}, false
		}
		definition.Type = "web_search"
		definition.Payload = map[string]any{"type": "web_search"}
		definition.BillingKeys = []string{"deepseek.tool_fetch"}
		return definition, true
	default:
		return Definition{}, false
	}
}
