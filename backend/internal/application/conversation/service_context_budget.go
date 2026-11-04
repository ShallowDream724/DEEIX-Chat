package conversation

import (
	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// contextBudgetPolicy evaluates the complete effective request without changing
// its messages. Only the conversation workflow can choose to rebuild it after
// compaction; every upstream attempt must fit the same route budget.
type contextBudgetPolicy struct {
	limit int64
}

func resolveContextBudgetPolicy(cfg config.Config, modelName, capabilitiesJSON string) contextBudgetPolicy {
	if !cfg.ContextTokenBudgetEnabled {
		return contextBudgetPolicy{}
	}
	return contextBudgetPolicy{limit: int64(domainchannel.EffectiveContextBudgetFromCapabilitiesWithFallback(
		modelName, capabilitiesJSON, cfg.ContextWindowFallbackTokens,
	))}
}

type contextBudgetDecision struct {
	limit, actual int64
	currentTurn   bool
}

func (p contextBudgetPolicy) evaluate(fullInput llm.GenerateInput, estimatedTokens int64) contextBudgetDecision {
	if p.limit <= 0 || estimatedTokens <= p.limit {
		return contextBudgetDecision{}
	}
	_, userIndex := toolHistoryBounds(fullInput.Messages)
	required := fullInput
	required.Messages = make([]llm.Message, 0, len(fullInput.Messages))
	hasHistory := false
	for index, message := range fullInput.Messages {
		if index >= userIndex || message.Role == "system" || message.Role == "developer" {
			required.Messages = append(required.Messages, message)
		} else {
			hasHistory = true
		}
	}
	if !hasHistory {
		return contextBudgetDecision{limit: p.limit, actual: estimatedTokens, currentTurn: true}
	}
	// A full-request measurement cannot be attributed to individual turns.
	// Estimate the current turn in isolation to decide whether a new chat
	// could help. Never use a clipped candidate as the outbound request.
	if requiredTokens := estimateGenerateInputTokens(required); requiredTokens > p.limit {
		return contextBudgetDecision{limit: p.limit, actual: requiredTokens, currentTurn: true}
	}
	return contextBudgetDecision{limit: p.limit, actual: estimatedTokens}
}

func (d contextBudgetDecision) err() error {
	if d.limit == 0 {
		return nil
	}
	dimension := "contextTokens"
	if d.currentTurn {
		dimension = "currentTurnTokens"
	}
	return llm.NewInputLimitError(dimension, d.actual, d.limit)
}

// Media limits apply to the same complete context, including history already
// retained by a provider when the wire request contains only a delta.
func validateGenerationInputMedia(route llm.RouteConfig, fullInput llm.GenerateInput) error {
	if err := llm.ValidateInputMedia(llm.EffectiveInputMediaLimits(route), fullInput); err != nil {
		return err
	}
	return llm.ValidateProviderInput(route, fullInput)
}
