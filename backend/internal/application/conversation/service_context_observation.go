package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/tokenestimate"
	"go.uber.org/zap"
)

const contextObservationVersion = 1

// contextInputObservation is an observation of an entire upstream request, never
// a token count attributed to an individual attachment. Only hashes and counts
// are persisted; media bytes, credentials and prompt text are not retained here.
type contextInputObservation struct {
	Version         int    `json:"version"`
	StableSignature string `json:"stable_signature"`
	EstimatedTokens int64  `json:"estimated_tokens"`
	InputTokens     int64  `json:"input_tokens"`
}

func contextObservationHash(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func contextObservationRouteIdentity(route *channel.ResolvedRoute, configSignature string) string {
	if route == nil {
		return ""
	}
	return contextObservationHash(struct {
		RouteID, UpstreamID                            uint
		Protocol, BaseURL, Model, Capabilities, Config string
	}{route.RouteID, route.UpstreamID, route.Protocol, route.BaseURL, route.UpstreamModel, route.ModelCapabilitiesJSON, configSignature})
}

// describeContextInput allows text additions/removals to be estimated against
// the measured request. Its stable signature requires the same system context,
// actual route/model, options, tools, and identical media occurrences/bytes.
// Moving a native part to the newest user message does not change its identity.
func describeContextInput(identity string, input llm.GenerateInput, fullMessages []llm.Message) contextInputObservation {
	input = fullContextObservationInput(input, fullMessages)
	type mediaPart struct {
		Kind, MIME, Name, Hash string
		SourceFileIDs          []string
		Pages                  int
		Duration               int64
	}
	media := make([]mediaPart, 0)
	var system []string
	if strings.TrimSpace(input.Instructions) != "" {
		system = append(system, strings.TrimSpace(input.Instructions))
	}
	for _, message := range input.Messages {
		if message.Role == "system" || message.Role == "developer" {
			if text := strings.TrimSpace(systemInstructionText(message)); text != "" {
				system = append(system, text)
			}
		}
		for _, part := range message.Parts {
			switch part.Kind {
			case llm.ContentPartFile:
				if len(part.SourceFileIDs) > 0 {
					hash := sha256.Sum256([]byte(part.Text))
					media = append(media, mediaPart{Kind: part.Kind, Hash: hex.EncodeToString(hash[:]), SourceFileIDs: part.SourceFileIDs})
				}
			case llm.ContentPartImage, llm.ContentPartDocument, llm.ContentPartAudio, llm.ContentPartVideo:
				hash := sha256.Sum256(part.Data)
				media = append(media, mediaPart{part.Kind, part.MimeType, part.FileName, hex.EncodeToString(hash[:]), part.SourceFileIDs, part.PageCount, part.DurationSeconds})
			}
		}
	}
	return contextInputObservation{
		Version: contextObservationVersion,
		StableSignature: contextObservationHash(struct {
			Identity, Instructions string
			Media                  []mediaPart
			Options                map[string]any
			Tools                  []llm.ToolDefinition
			DisableTools           bool
		}{identity, strings.Join(system, "\n\n"), media, input.Options, input.Tools, input.DisableTools}),
		EstimatedTokens: estimateGenerateInputTokens(input),
	}
}

func fullContextObservationInput(input llm.GenerateInput, fullMessages []llm.Message) llm.GenerateInput {
	if input.PreviousResponseID != "" {
		input.Messages = fullMessages
		input.PreviousResponseID = ""
		if strings.TrimSpace(input.Instructions) != "" {
			_, input.Messages = extractOpenAIResponsesInstructions(input.Messages)
		}
	}
	return input
}

func (o contextInputObservation) estimate(current contextInputObservation) (int64, bool) {
	if o.Version != contextObservationVersion || o.InputTokens <= 0 || o.StableSignature == "" || o.StableSignature != current.StableSignature {
		return current.EstimatedTokens, false
	}
	return max(o.InputTokens+current.EstimatedTokens-o.EstimatedTokens, 0), true
}

func (r *messageGenerationRunner) estimateContextInput(input llm.GenerateInput) int64 {
	current := describeContextInput(r.contextIdentity, input, nil)
	for index := len(r.contextObservations) - 1; index >= 0; index-- {
		if tokens, ok := r.contextObservations[index].estimate(current); ok {
			return tokens
		}
	}
	if r.contextInputEstimator != nil {
		return r.contextInputEstimator(input)
	}
	return current.EstimatedTokens
}

// contextInputEstimator reads only measurements owned by assistants on this
// ancestor path. A snapshot boundary intentionally does not limit this lookup:
// its changed system summary already invalidates incompatible observations.
func (s *Service) contextInputEstimator(ctx context.Context, scope repository.HistoricalMessageScope, identity string) func(llm.GenerateInput) int64 {
	var observations []contextInputObservation
	if s != nil && s.repo != nil && scope.Valid() {
		items, err := s.repo.ListRecentContextArtifacts(ctx, repository.ContextArtifactListFilter{
			Scope: scope, Kinds: []model.ContextArtifactKind{model.ContextArtifactInputObservation}, Limit: 20,
		})
		if err == nil {
			for _, item := range items {
				var observation contextInputObservation
				if json.Unmarshal([]byte(item.MetadataJSON), &observation) == nil {
					observations = append(observations, observation)
				}
			}
		}
	}
	return func(input llm.GenerateInput) int64 {
		current := describeContextInput(identity, input, nil)
		for _, observed := range observations {
			if tokens, ok := observed.estimate(current); ok {
				return tokens
			}
		}
		return current.EstimatedTokens
	}
}

// contextTokenEstimate reports the latest request's context, independently of
// the sum of billed calls. Only output text actually retained by the application
// is added; hidden reasoning/output already included by providers is not added
// a second time as a separate billing counter.
func (r *messageGenerationRunner) contextTokenEstimate(text, reasoning string, fallback int64) int64 {
	inputTokens := r.usage.latestContextInputTokens(fallback)
	return inputTokens + tokenestimate.Estimate(text) + tokenestimate.Estimate(reasoning)
}

func (r *messageGenerationRunner) persistContextObservation(ctx context.Context, assistant *model.Message) {
	if r == nil || r.service == nil || r.service.repo == nil || assistant == nil || assistant.ID == 0 || len(r.contextObservations) == 0 {
		return
	}
	items := make([]model.ContextArtifact, 0, len(r.contextObservations))
	for _, observation := range r.contextObservations {
		raw, err := json.Marshal(observation)
		if err != nil {
			continue
		}
		items = append(items, model.ContextArtifact{
			ConversationID: r.input.ConversationID, UserID: r.input.UserID, MessageID: assistant.ID, RunID: r.runID,
			Kind: model.ContextArtifactInputObservation, SourceType: "upstream_request", SourceID: observation.StableSignature,
			SourceTitle: "Upstream input observation", MetadataJSON: string(raw), TokenEstimate: observation.InputTokens,
		})
	}
	r.service.applyContextArtifactRetention(items)
	if err := r.service.repo.CreateContextArtifacts(ctx, items); err != nil && r.service.logger != nil {
		r.service.logger.Warn("context_input_observation_persist_failed", zap.Uint("message_id", assistant.ID), zap.Error(err))
	}
}
