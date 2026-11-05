package conversation

import (
	"encoding/json"
	"strings"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func syncUpstreamOutputThinking(traceRecorder *messageTraceRecorder, output *llm.GenerateOutput) string {
	if output == nil {
		return ""
	}
	assistantText, extractedThink := output.Text, ""
	if !output.ContentNormalized {
		assistantText, extractedThink = splitAssistantOutputThinkingContent(output.Text)
	}
	if assistantText == "" && strings.TrimSpace(extractedThink) == "" {
		assistantText = strings.TrimSpace(output.Text)
	}
	if traceRecorder != nil && output.Reasoning != nil {
		syncFinalReasoningTrace(traceRecorder, output.Reasoning, false)
	} else if traceRecorder != nil && strings.TrimSpace(extractedThink) != "" {
		traceRecorder.syncStructuredThink(extractedThink, "", nil)
	}
	if traceRecorder != nil {
		traceRecorder.completeUpstreamThink()
	}
	return assistantText
}

func syncUpstreamOutputTrace(traceRecorder *messageTraceRecorder, output *llm.GenerateOutput, runID string) (string, []model.ToolCall) {
	if output == nil {
		return "", nil
	}
	if len(output.ProcessOrder) > 0 {
		rows := upstreamServerToolCallRows(output, runID)
		syncOrderedOutputProcess(traceRecorder, output, rows)
		// Extract visible text without replaying aggregate reasoning after the
		// ordered segment references have already been consumed.
		return syncUpstreamOutputThinking(nil, output), rows
	}
	// 原生 server-side tools 是上游在同一次 Responses 调用内部完成的工具。
	// 当本轮没有本地函数调用时，先记录工具再记录最终 reasoning，避免 UI 看起来缺少工具后的最后一次思考。
	var serverToolRows []model.ToolCall
	if shouldSyncServerToolsBeforeThinking(output) {
		serverToolRows = upstreamServerToolCallRows(output, runID)
		appendUpstreamServerToolTrace(traceRecorder, serverToolRows)
		return syncUpstreamOutputThinking(traceRecorder, output), serverToolRows
	}
	assistantText := syncUpstreamOutputThinking(traceRecorder, output)
	serverToolRows = upstreamServerToolCallRows(output, runID)
	appendUpstreamServerToolTrace(traceRecorder, serverToolRows)
	return assistantText, serverToolRows
}

func syncOrderedOutputProcess(recorder *messageTraceRecorder, output *llm.GenerateOutput, rows []model.ToolCall) {
	if recorder == nil {
		return
	}
	var segments []llm.ReasoningOutput
	if output.Reasoning != nil {
		segments = output.Reasoning.Segments
	}
	reasoningIndices := make(map[string]int, len(segments))
	toolIndices := make(map[string]int, len(rows))
	for index, segment := range segments {
		if segment.ItemID != "" {
			reasoningIndices[segment.ItemID] = index
		}
	}
	for index, row := range rows {
		if row.ToolCallID != "" {
			toolIndices[row.ToolCallID] = index
		}
	}
	usedReasoning, usedTools := make([]bool, len(segments)), make([]bool, len(rows))
	recordReasoning := func(index int) {
		if usedReasoning[index] {
			return
		}
		usedReasoning[index] = true
		syncFinalReasoningTrace(recorder, &segments[index], false)
		recorder.completeUpstreamThink()
	}
	recordTool := func(index int) {
		if usedTools[index] {
			return
		}
		usedTools[index] = true
		appendUpstreamServerToolTrace(recorder, rows[index:index+1])
	}
	for _, item := range output.ProcessOrder {
		if index, found := reasoningIndices[item.ReasoningItemID]; found {
			recordReasoning(index)
		}
		if index, found := toolIndices[item.ToolCallID]; found {
			recordTool(index)
		}
	}
	// A partial reference list must not drop otherwise valid process records.
	for index := range segments {
		recordReasoning(index)
	}
	for index := range rows {
		recordTool(index)
	}
}

// finalizeStreamingOutputTrace 对最终推理快照进行对账，且不
// 重放流中已发出的 trace 事件。部分适配器仅在最终输出中
// 暴露服务端工具；当某次调用未观察到实时服务端工具事件时，
// 这些行恰好补充一次。
func finalizeStreamingOutputTrace(traceRecorder *messageTraceRecorder, output *llm.GenerateOutput, runID string, observedServerTools map[string]string) {
	if traceRecorder == nil || output == nil {
		return
	}
	serverToolRows := unreconciledServerToolRows(upstreamServerToolCallRows(output, runID), observedServerTools)
	if shouldSyncServerToolsBeforeThinking(output) {
		appendUpstreamServerToolTrace(traceRecorder, serverToolRows)
	}
	if output.Reasoning != nil {
		syncFinalReasoningTrace(traceRecorder, output.Reasoning, true)
	}
	traceRecorder.completeUpstreamThink()
	if !shouldSyncServerToolsBeforeThinking(output) {
		appendUpstreamServerToolTrace(traceRecorder, serverToolRows)
	}
}

func syncFinalReasoningTrace(recorder *messageTraceRecorder, reasoning *llm.ReasoningOutput, streamed bool) {
	if recorder == nil || reasoning == nil {
		return
	}
	segments := reasoning.Segments
	if len(segments) == 0 {
		segments = []llm.ReasoningOutput{*reasoning}
	}
	for _, segment := range segments {
		payload := reasoningPayload(&llm.ReasoningDelta{EventType: "response.completed", ItemID: segment.ItemID, Status: segment.Status, Kind: messageTraceThinkKindContent, EncryptedContent: segment.EncryptedContent})
		if streamed {
			recorder.reconcileStructuredThink(segment.Text, segment.Summary, payload)
		} else {
			recorder.syncStructuredThink(segment.Text, segment.Summary, payload)
		}
		if len(reasoning.Segments) > 0 {
			recorder.completeUpstreamThink()
		}
	}
}

func observeServerTool(statuses map[string]string, call llm.ToolCall, status string) {
	if statuses == nil {
		return
	}
	key := serverToolTraceKey(call.ToolCallID, call.ToolType, call.ToolName)
	if key == "" {
		return
	}
	statuses[key] = normalizeStreamServerToolStatus(status)
}

// syncStreamingServerToolTrace shares server-process delivery between ordinary
// and temporary conversations. These records never enter the local tool queue.
func syncStreamingServerToolTrace(recorder *messageTraceRecorder, call llm.ToolCall, runID string, observed map[string]string) {
	if recorder == nil {
		return
	}
	// A throttled reasoning delta must obtain its snapshot position before the
	// following tool record, without declaring that reasoning has completed.
	recorder.flushUpstreamThinkLiveUpdate(recorder.upstreamThink, upstreamThinkLiveUpdateOptions{PersistSnapshot: true})
	status := normalizeStreamServerToolStatus(call.Status)
	observeServerTool(observed, call, status)
	summary, markdown, payload := buildToolTrace([]model.ToolCall{{RunID: strings.TrimSpace(runID), ToolCallID: strings.TrimSpace(call.ToolCallID), ToolType: strings.TrimSpace(call.ToolType), ToolName: strings.TrimSpace(call.ToolName), Status: status, InputJSON: strings.TrimSpace(call.ArgumentsJSON), OutputJSON: strings.TrimSpace(call.OutputJSON), ErrorJSON: strings.TrimSpace(call.ErrorJSON)}})
	recorder.syncToolSection(summary, markdown, payload, traceStatusFromToolStatus(status))
}

func unreconciledServerToolRows(rows []model.ToolCall, observed map[string]string) []model.ToolCall {
	if len(rows) == 0 {
		return nil
	}
	pending := make([]model.ToolCall, 0, len(rows))
	for _, row := range rows {
		key := serverToolTraceKey(row.ToolCallID, row.ToolType, row.ToolName)
		status, found := observed[key]
		if found && isTerminalServerToolStatus(status) {
			continue
		}
		if found && status == "incomplete" && strings.TrimSpace(row.Status) == "incomplete" && hasChat01ToolProvenance(row.ToolCallID, row.InputJSON) {
			// Chat01 streams each updated process record through its final parser
			// delta. Replaying its snapshot into the last round would duplicate
			// records already attached to earlier reasoning rounds.
			continue
		}
		pending = append(pending, row)
	}
	return pending
}

func serverToolTraceKey(toolCallID string, toolType string, toolName string) string {
	if value := strings.TrimSpace(toolCallID); value != "" {
		return "id:" + value
	}
	typeKey := strings.ToLower(strings.TrimSpace(toolType))
	nameKey := strings.ToLower(strings.TrimSpace(toolName))
	if typeKey == "" && nameKey == "" {
		return ""
	}
	return "kind:" + typeKey + "\x00" + nameKey
}

func isTerminalServerToolStatus(status string) bool {
	switch normalizeStreamServerToolStatus(status) {
	case "success", "error":
		return true
	default:
		return false
	}
}

func outputReasoningContent(output *llm.GenerateOutput) string {
	if output == nil {
		return ""
	}
	if output.Reasoning != nil {
		if text := strings.TrimSpace(output.Reasoning.Text); text != "" {
			return text
		}
	}
	if output.ContentNormalized {
		return ""
	}
	_, extractedThink := splitAssistantOutputThinkingContent(output.Text)
	return strings.TrimSpace(extractedThink)
}

func shouldSyncServerToolsBeforeThinking(output *llm.GenerateOutput) bool {
	return output != nil && len(output.ServerToolCalls) > 0 && len(output.ToolCalls) == 0
}

func upstreamServerToolCallRows(output *llm.GenerateOutput, runID string) []model.ToolCall {
	if output == nil || len(output.ServerToolCalls) == 0 {
		return nil
	}
	rows := make([]model.ToolCall, 0, len(output.ServerToolCalls))
	for _, item := range output.ServerToolCalls {
		status := strings.TrimSpace(item.Status)
		switch status {
		case "", "completed":
			status = "success"
		case "in_progress", "queued":
			status = "streaming"
		}
		outputJSON := strings.TrimSpace(item.OutputJSON)
		if outputJSON == "" && isSearchServerToolCall(item) && canAttachResponseCitationsToTool(item) {
			outputJSON = citationsToolOutputJSON(output.Citations)
		}
		rows = append(rows, model.ToolCall{
			RunID:      strings.TrimSpace(runID),
			ToolCallID: strings.TrimSpace(item.ToolCallID),
			ToolType:   strings.TrimSpace(item.ToolType),
			ToolName:   strings.TrimSpace(item.ToolName),
			Status:     status,
			InputJSON:  strings.TrimSpace(item.ArgumentsJSON),
			OutputJSON: outputJSON,
			ErrorJSON:  strings.TrimSpace(item.ErrorJSON),
		})
	}
	return rows
}

func canAttachResponseCitationsToTool(call llm.ToolCall) bool {
	if strings.EqualFold(strings.TrimSpace(call.Status), "incomplete") {
		return false
	}
	return !hasChat01ToolProvenance(call.ToolCallID, call.ArgumentsJSON)
}

func hasChat01ToolProvenance(callID, argsJSON string) bool {
	if strings.HasPrefix(strings.TrimSpace(callID), "chat01:") {
		return true
	}
	var args struct {
		Provider string `json:"provider"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)
	return strings.EqualFold(strings.TrimSpace(args.Provider), "chat01")
}

func appendUpstreamServerToolTrace(traceRecorder *messageTraceRecorder, rows []model.ToolCall) {
	if traceRecorder == nil || len(rows) == 0 {
		return
	}
	summary, markdown, payload := buildToolTrace(rows)
	traceRecorder.appendToolSection(summary, markdown, payload, messageTraceStatusCompleted)
}

func normalizeStreamServerToolStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "", "completed", "success":
		return "success"
	case "in_progress", "queued", "searching", "generating":
		return "streaming"
	case "failed", "error":
		return "error"
	default:
		return strings.TrimSpace(status)
	}
}

func traceStatusFromToolStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "streaming", "requested":
		return messageTraceStatusStreaming
	case "error", "failed":
		return messageTraceStatusError
	default:
		return messageTraceStatusCompleted
	}
}

func isSearchServerToolCall(item llm.ToolCall) bool {
	toolType := strings.ToLower(strings.TrimSpace(item.ToolType))
	toolName := strings.ToLower(strings.TrimSpace(item.ToolName))
	return strings.Contains(toolType, "search") || strings.Contains(toolName, "search")
}

func citationsToolOutputJSON(citations []string) string {
	if len(citations) == 0 {
		return ""
	}
	items := make([]map[string]string, 0, len(citations))
	for _, citation := range citations {
		if value := strings.TrimSpace(citation); value != "" {
			items = append(items, map[string]string{"url": value})
		}
	}
	if len(items) == 0 {
		return ""
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	return string(payload)
}

func addLLMUsage(left llm.Usage, right llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:        left.InputTokens + right.InputTokens,
		OutputTokens:       left.OutputTokens + right.OutputTokens,
		CacheReadTokens:    left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens:   left.CacheWriteTokens + right.CacheWriteTokens,
		CacheWrite5mTokens: left.CacheWrite5mTokens + right.CacheWrite5mTokens,
		CacheWrite1hTokens: left.CacheWrite1hTokens + right.CacheWrite1hTokens,
		ReasoningTokens:    left.ReasoningTokens + right.ReasoningTokens,
		Speed:              mergeLLMUsageSpeed(left.Speed, right.Speed),
		ServiceTier:        mergeLLMUsageServiceTier(left.ServiceTier, right.ServiceTier),
		RawUsageJSON:       llm.MergeRawUsageJSON(left.RawUsageJSON, right.RawUsageJSON),
	}
}

func diffLLMUsage(current llm.Usage, previous llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:        nonNegativeTokenDelta(current.InputTokens, previous.InputTokens),
		OutputTokens:       nonNegativeTokenDelta(current.OutputTokens, previous.OutputTokens),
		CacheReadTokens:    nonNegativeTokenDelta(current.CacheReadTokens, previous.CacheReadTokens),
		CacheWriteTokens:   nonNegativeTokenDelta(current.CacheWriteTokens, previous.CacheWriteTokens),
		CacheWrite5mTokens: nonNegativeTokenDelta(current.CacheWrite5mTokens, previous.CacheWrite5mTokens),
		CacheWrite1hTokens: nonNegativeTokenDelta(current.CacheWrite1hTokens, previous.CacheWrite1hTokens),
		ReasoningTokens:    nonNegativeTokenDelta(current.ReasoningTokens, previous.ReasoningTokens),
		Speed:              strings.TrimSpace(current.Speed),
		ServiceTier:        strings.TrimSpace(current.ServiceTier),
		RawUsageJSON:       diffLLMUsageRawJSON(current.RawUsageJSON, previous.RawUsageJSON),
	}
}

func nonNegativeTokenDelta(current int64, previous int64) int64 {
	if current <= previous {
		return 0
	}
	return current - previous
}

func emitLLMUsageEvent(onEvent func(eventType string, payload map[string]any) error, usage llm.Usage) error {
	if onEvent == nil || usage == (llm.Usage{}) {
		return nil
	}
	return onEvent("usage", map[string]any{
		"input_tokens":       usage.InputTokens,
		"output_tokens":      usage.OutputTokens,
		"cache_read_tokens":  usage.CacheReadTokens,
		"cache_write_tokens": usage.CacheWriteTokens,
		"reasoning_tokens":   usage.ReasoningTokens,
	})
}

func addServerSideToolUsage(left map[string]int64, right map[string]int64) map[string]int64 {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	result := make(map[string]int64, len(left)+len(right))
	for key, value := range left {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey != "" && value > 0 {
			result[normalizedKey] += value
		}
	}
	for key, value := range right {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey != "" && value > 0 {
			result[normalizedKey] += value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func diffLLMUsageRawJSON(current string, previous string) string {
	current = strings.TrimSpace(current)
	if current == "" || current == strings.TrimSpace(previous) {
		return ""
	}
	return current
}

func mergeLLMUsageSpeed(left string, right string) string {
	left = strings.TrimSpace(strings.ToLower(left))
	right = strings.TrimSpace(strings.ToLower(right))
	if left == "fast" || right == "fast" {
		return "fast"
	}
	if right != "" {
		return right
	}
	return left
}

func mergeLLMUsageServiceTier(left string, right string) string {
	left = strings.TrimSpace(strings.ToLower(left))
	right = strings.TrimSpace(strings.ToLower(right))
	if right != "" {
		return right
	}
	return left
}

func buildFinalToolSynthesisMessages(messages []llm.Message, instruction string) []llm.Message {
	result := make([]llm.Message, 0, len(messages)+1)
	result = append(result, messages...)
	result = append(result, llm.Message{
		Role:    "system",
		Content: strings.TrimSpace(instruction),
	})
	return result
}

// toolRunFinalAnswerMissing 判断工具循环在预算耗尽时是否只剩未执行的结构化工具调用。
func toolRunFinalAnswerMissing(output *llm.GenerateOutput, toolLoopStarted bool, llmCallCount int, maxLLMCalls int, remainingToolCalls int) bool {
	if output == nil || !toolLoopStarted {
		return false
	}
	return len(output.ToolCalls) > 0 && (llmCallCount >= maxLLMCalls || remainingToolCalls <= 0)
}
