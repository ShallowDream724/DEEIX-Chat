package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// A date belongs to the user request, not to each retry or tool call. Resolve
// the profile lazily so unrelated routes do not perform another database read.
func (s *Service) requestDateContext(ctx context.Context, userID uint, startedAt time.Time) func() string {
	return sync.OnceValue(func() string {
		zone := "UTC"
		if s.repo != nil && userID != 0 {
			if user, err := s.repo.GetUserByID(ctx, userID); err == nil && user != nil {
				zone = user.Timezone
			}
		}
		return formatRequestDateContext(startedAt, zone)
	})
}

func formatRequestDateContext(at time.Time, zone string) string {
	zone = strings.TrimSpace(zone)
	location, err := time.LoadLocation(zone)
	// Never inherit the deployment host's timezone (including Go's "Local").
	if err != nil || zone == "" || zone == "Local" {
		location = time.UTC
	}
	local := at.In(location)
	return fmt.Sprintf("Current request date: %s (%s). Timezone: %s (UTC%s).\nResolve relative dates such as today using this date, unless the user specifies a different date. For time-sensitive searches, use the relevant absolute date and verify the dates of the sources; do not infer today's date from an old search result.",
		local.Format("2006-01-02"), local.Weekday(), location.String(), local.Format("-07:00"))
}

func deepSeekRequestDateEnabled(route *channel.ResolvedRoute) bool {
	if route == nil {
		return false
	}
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterDeepSeekResponses:
		return true
	case llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions, llm.AdapterAnthropicMessages:
	default:
		return false
	}
	endpoint, err := url.Parse(route.BaseURL)
	if err == nil && strings.EqualFold(endpoint.Hostname(), "api.deepseek.com") {
		return true
	}
	var capabilities struct {
		NativeFiles struct {
			Provider string `json:"provider"`
		} `json:"nativeFiles"`
		NativeToolKeys []string `json:"nativeToolKeys"`
		NativeTools    []struct {
			Key     string `json:"key"`
			ToolKey string `json:"toolKey"`
		} `json:"nativeTools"`
	}
	if json.Unmarshal([]byte(route.ModelCapabilitiesJSON), &capabilities) != nil {
		return false
	}
	if strings.EqualFold(capabilities.NativeFiles.Provider, "deepseek") {
		return true
	}
	isDeepSeekKey := func(key string) bool { return strings.HasPrefix(strings.TrimSpace(key), "deepseek.") }
	for _, key := range capabilities.NativeToolKeys {
		if isDeepSeekKey(key) {
			return true
		}
	}
	for _, tool := range capabilities.NativeTools {
		if isDeepSeekKey(tool.Key) || isDeepSeekKey(tool.ToolKey) {
			return true
		}
	}
	return false
}

// All supported DeepSeek transports accept system messages. In particular,
// Responses does not accept the latest_reminder role; do not send that role or
// change the user's saved prompt. Day precision keeps the prefix stable all day.
func withDeepSeekRequestDate(messages []llm.Message, route *channel.ResolvedRoute, resolve func() string) []llm.Message {
	if resolve == nil || !deepSeekRequestDateEnabled(route) {
		return messages
	}
	content := resolve()
	if content == "" {
		return messages
	}
	// Keep the stable system policy first. The date is runtime context, not a
	// replacement for any administrator, model or project instruction.
	index := 0
	for index < len(messages) && messages[index].Role == "system" {
		index++
	}
	result := make([]llm.Message, 0, len(messages)+1)
	result = append(result, messages[:index]...)
	result = append(result, llm.Message{Role: "system", Content: content})
	return append(result, messages[index:]...)
}
