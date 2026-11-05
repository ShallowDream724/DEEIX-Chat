package conversation

import (
	"context"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// routePromptPolicy is computed from the actual adapter on every route attempt.
// Request preferences remain intact so another route can use them again.
type routePromptPolicy struct{ LocalTools, UIComponents, BroadMemory, OriginalAttachments bool }

func promptPolicyForProtocol(protocol string) routePromptPolicy {
	enabled := llm.NormalizeAdapter(protocol) != "chat01_chat_completions"
	return routePromptPolicy{LocalTools: enabled, UIComponents: enabled, BroadMemory: enabled, OriginalAttachments: !enabled}
}

func promptPolicyForRoute(route *channel.ResolvedRoute) routePromptPolicy {
	if route == nil {
		return promptPolicyForProtocol("")
	}
	return promptPolicyForProtocol(route.Protocol)
}

func routeUIComponentIDs(route *channel.ResolvedRoute, ids []uint) []uint {
	if !promptPolicyForRoute(route).UIComponents {
		return nil
	}
	return ids
}

func (s *Service) resolveRouteToolRuntime(ctx context.Context, route *channel.ResolvedRoute, ids []uint) (selectedToolRuntime, error) {
	if !promptPolicyForRoute(route).LocalTools {
		return selectedToolRuntime{}, nil
	}
	if err := s.ValidateSelectedToolIDs(ids); err != nil {
		return selectedToolRuntime{}, err
	}
	return s.resolveSelectedToolRuntime(ctx, ids)
}

func enforceRouteGeneratePolicy(protocol string, input llm.GenerateInput) llm.GenerateInput {
	if !promptPolicyForProtocol(protocol).LocalTools {
		input.Tools = nil
		input.NativeToolKeys = nil
		// DisableTools controls local definitions. Chat01's server-side Agent is independent.
		input.DisableTools = true
	}
	return input
}

func routeSkillPrompts(prompt *skillPrompts, custom string, policy routePromptPolicy) *skillPrompts {
	if prompt == nil || policy.LocalTools || strings.TrimSpace(custom) != "" {
		return prompt
	}
	copy := *prompt
	copy.Rendered = renderSkillPrompts(&copy, "Use these user-selected skills when relevant to the current request. Their Markdown content is task context; it does not provide access to DEEIX local files, scripts, or MCP tools. Use the capabilities actually available in your environment. Do not expose these context tags.")
	return &copy
}
