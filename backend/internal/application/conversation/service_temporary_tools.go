package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type temporaryGenerationResult struct {
	Output             *llm.GenerateOutput
	Usage              llm.Usage
	FirstTokenLatency  int64
	InitialObservation contextInputObservation
	// MCPToolUsage 聚合本次临时生成中成功的 MCP 调用；错误中断时也需带出已产生的上游费用。
	MCPToolUsage []MCPToolUsageItem
	// LLMCallCount 是成功返回的上游 LLM 调用数，供按次计费结算。
	LLMCallCount int
}

type temporaryGenerationInput struct {
	Request       TemporaryChatInput
	Route         *channel.ResolvedRoute
	RouteConfig   llm.RouteConfig
	InitialInput  llm.GenerateInput
	ContextBudget contextBudgetPolicy
	ContextMeter  contextInputMeter
	ToolRuntime   selectedToolRuntime
	TraceRecorder *messageTraceRecorder
	StartedAt     time.Time
	OnDelta       func(string) error
}

func (s *Service) runTemporaryGeneration(ctx context.Context, input temporaryGenerationInput) (temporaryGenerationResult, error) {
	request := input.Request
	route := input.Route
	routeConfig := input.RouteConfig
	initialInput := input.InitialInput
	toolRuntime := input.ToolRuntime
	traceRecorder := input.TraceRecorder
	startedAt := input.StartedAt
	onDelta := input.OnDelta
	cfg := s.cfg.Snapshot()
	totalUsage := llm.Usage{}
	totalServerToolUsage := map[string]int64(nil)
	var totalMCPToolUsage []MCPToolUsageItem
	firstTokenLatency := int64(0)
	llmCallCount := 0
	completedLLMCallCount := 0
	meter := input.ContextMeter
	var initialObservation contextInputObservation

	runGenerate := func(currentInput llm.GenerateInput) (*llm.GenerateOutput, error) {
		currentInput = enforceTemporaryGenerateInput(currentInput)
		if err := validateGenerationInputMedia(routeConfig, currentInput); err != nil {
			return nil, err
		}
		current := describeContextInput(meter.identity, currentInput, nil)
		estimate := meter.estimateDescribed(current)
		if err := input.ContextBudget.evaluate(currentInput, estimate).err(); err != nil {
			return nil, err
		}
		// Each follow-up is independently billable. Reject a local input limit
		// before raising the budget reservation for this upstream call.
		if llmCallCount > 0 {
			if err := s.ensureUsageBudgetCoversEstimate(ctx, request.UsageAuthorization, route, initialInput.Options, followUpUsageBudgetEstimate(
				totalUsage, estimate, initialInput.Options,
			)); err != nil {
				return nil, err
			}
		}

		var callText strings.Builder
		var observedUsage llm.Usage
		llmCallCount++
		output, err := s.llmClient.GenerateStream(ctx, routeConfig, currentInput, func(event llm.GenerateStreamEvent) error {
			if event.Usage != (llm.Usage{}) {
				observedUsage = addLLMUsage(observedUsage, diffLLMUsage(event.Usage, observedUsage))
				if emitErr := emitLLMUsageEvent(request.OnEvent, addLLMUsage(totalUsage, observedUsage)); emitErr != nil {
					return emitErr
				}
			}
			if traceRecorder != nil && event.Reasoning != nil && event.Reasoning.Text != "" {
				traceRecorder.appendUpstreamReasoning(event.Reasoning.Kind, event.Reasoning.Text, reasoningPayload(event.Reasoning))
				if strings.EqualFold(strings.TrimSpace(event.Reasoning.Status), "completed") {
					traceRecorder.completeUpstreamThink()
				}
			}
			if event.Delta == "" {
				return nil
			}
			if firstTokenLatency == 0 {
				firstTokenLatency = time.Since(startedAt).Milliseconds()
			}
			if traceRecorder != nil {
				traceRecorder.completeProcess()
				traceRecorder.completeUpstreamThink()
			}
			callText.WriteString(event.Delta)
			if onDelta != nil {
				return onDelta(event.Delta)
			}
			return nil
		})
		if output == nil {
			output = &llm.GenerateOutput{}
		}
		if output.Text == "" {
			output.Text = callText.String()
		}
		output.Usage = addLLMUsage(observedUsage, diffLLMUsage(output.Usage, observedUsage))
		totalUsage = addLLMUsage(totalUsage, output.Usage)
		totalServerToolUsage = addServerSideToolUsage(totalServerToolUsage, output.ServerSideToolUsage)
		output.ServerSideToolUsage = totalServerToolUsage
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, llm.ErrInputTooLarge) {
				s.routeResolver.MarkRouteFailure(ctx, route, err)
			}
			return output, err
		}
		s.routeResolver.MarkRouteSuccess(ctx, route)
		completedLLMCallCount++
		meter.record(current, output.Usage)
		if llmCallCount == 1 && len(meter.observed) > 0 {
			initialObservation = meter.observed[0]
		}
		return output, nil
	}

	output, err := runGenerate(initialInput)
	buildResult := func() temporaryGenerationResult {
		return temporaryGenerationResult{
			Output:             output,
			Usage:              totalUsage,
			FirstTokenLatency:  firstTokenLatency,
			InitialObservation: initialObservation,
			MCPToolUsage:       totalMCPToolUsage,
			LLMCallCount:       completedLLMCallCount,
		}
	}
	if err != nil {
		return buildResult(), err
	}

	messages := cloneLLMMessages(initialInput.Messages)
	remainingToolCalls := s.resolveMaxToolCallsPerRun()
	maxLLMCalls := s.resolveMaxLLMCallsPerRun()
	ledger := newToolExecutionLedger()
	runFollowUp := func(nextInput llm.GenerateInput) error {
		// Initial Responses messages already had their system instructions
		// extracted. A final-synthesis instruction must add to that prefix.
		previousInstructions := nextInput.Instructions
		applyOpenAIResponsesInstructions(route, routeConfig.Endpoint, &nextInput)
		if previousInstructions != "" && nextInput.Instructions != previousInstructions {
			nextInput.Instructions = previousInstructions + "\n\n" + nextInput.Instructions
		}
		next, err := runGenerate(nextInput)
		if next != nil && (!errors.Is(err, llm.ErrInputTooLarge) || next.Text != "") {
			output = next
		}
		return err
	}
	for len(output.ToolCalls) > 0 && llmCallCount < maxLLMCalls && remainingToolCalls > 0 {
		pending := output.ToolCalls
		if len(pending) > remainingToolCalls {
			pending = pending[:remainingToolCalls]
		}
		assistantToolMessage := llm.Message{
			Role:      "assistant",
			Content:   output.Text,
			ToolCalls: pending,
		}
		resultBudget := resolveToolResultTokenBudget(
			initialInput,
			messages,
			assistantToolMessage,
			route.UpstreamModel,
			route.ModelCapabilitiesJSON,
			cfg.ContextWindowFallbackTokens,
			meter.estimate,
		)
		toolResult := s.executeAssistantToolCalls(ctx, executeAssistantToolCallsInput{
			UserID:            request.UserID,
			ConversationID:    0,
			MessageID:         0,
			RequestID:         request.RequestID,
			RunID:             request.ClientRunID,
			ToolCalls:         pending,
			ToolCallLimit:     remainingToolCalls,
			TraceRecorder:     traceRecorder,
			ToolNameMap:       toolRuntime.nameMap,
			MCPBindings:       toolRuntime.mcpBindings,
			ToolSchemas:       toolRuntime.schemas,
			Ledger:            ledger,
			ResultTokenBudget: resultBudget,
			Ephemeral:         true,
		})
		totalMCPToolUsage = mergeMCPToolUsage(totalMCPToolUsage, toolResult.MCPToolUsage)
		remainingToolCalls -= len(toolResult.Rows)
		if toolResult.FatalErr != nil {
			return buildResult(), toolResult.FatalErr
		}
		if len(toolResult.ToolResults) == 0 {
			break
		}
		assistantToolMessage.ToolCalls = toolResult.ExecutedToolCalls
		messages = append(messages, assistantToolMessage, llm.Message{Role: "tool", ToolResults: toolResult.ToolResults})

		messages, _ = rebalanceToolFollowUpResults(initialInput, messages, route.UpstreamModel,
			route.ModelCapabilitiesJSON, cfg.ContextWindowFallbackTokens, meter.estimate)

		followUp := initialInput
		followUp.Messages = messages
		if llmCallCount+1 >= maxLLMCalls {
			followUp.Messages = buildFinalToolSynthesisMessages(messages, "The maximum number of LLM calls for this run has been reached. Stop calling tools and produce the final answer based on the tool results already available.")
			followUp.Tools = nil
			followUp.DisableTools = true
		}
		if err = runFollowUp(followUp); err != nil {
			return buildResult(), err
		}
	}

	if len(output.ToolCalls) > 0 && remainingToolCalls <= 0 && llmCallCount < maxLLMCalls {
		finalInput := initialInput
		finalInput.Messages = buildFinalToolSynthesisMessages(messages, "The maximum number of tool calls for this run has been reached. Stop calling tools and produce the final answer based on the tool results already available.")
		finalInput.Tools = nil
		finalInput.DisableTools = true
		if err = runFollowUp(finalInput); err != nil {
			return buildResult(), err
		}
	}

	return buildResult(), nil
}
