package conversation

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/background"
	"go.uber.org/zap"
)

const conversationMetadataRecoveryAttempts = 3
const conversationMetadataCompletedJobs = 1024

type conversationMetadataRecoveryKey struct {
	conversationID uint
	messageID      uint
	runID          string
}

// This bounded process-local registry suppresses simultaneous scheduling and recent
// post-billing replays. It is not a durable queue: restart does not replay failures.
type conversationMetadataRecoveryRegistry struct {
	mu        sync.Mutex
	active    map[uint]bool
	completed map[conversationMetadataRecoveryKey]bool
	order     []conversationMetadataRecoveryKey
}

func (r *conversationMetadataRecoveryRegistry) start(key conversationMetadataRecoveryKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[key.conversationID] || r.completed[key] {
		return false
	}
	if r.active == nil {
		r.active = make(map[uint]bool)
		r.completed = make(map[conversationMetadataRecoveryKey]bool)
	}
	r.active[key.conversationID] = true
	return true
}

func (r *conversationMetadataRecoveryRegistry) finish(key conversationMetadataRecoveryKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, key.conversationID)
	r.completed[key] = true
	r.order = append(r.order, key)
	if len(r.order) > conversationMetadataCompletedJobs {
		delete(r.completed, r.order[0])
		r.order = r.order[1:]
	}
}

type conversationMetadataRecoveryOptions struct {
	attemptTimeout time.Duration
	wait           func(context.Context, time.Duration) error
}

func defaultConversationMetadataRecoveryOptions() conversationMetadataRecoveryOptions {
	return conversationMetadataRecoveryOptions{
		attemptTimeout: conversationMetadataGenerationTimeout,
		wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

type retryableConversationMetadataError struct{ cause error }

func (e *retryableConversationMetadataError) Error() string { return e.cause.Error() }
func (e *retryableConversationMetadataError) Unwrap() error { return e.cause }

type conversationMetadataBillingError struct{ cause error }

func (e *conversationMetadataBillingError) Error() string { return e.cause.Error() }
func (e *conversationMetadataBillingError) Unwrap() error { return e.cause }

func conversationMetadataRequestResultUnknown(err error) bool {
	if llm.RequestWasAccepted(err) {
		return true
	}
	var upstream *llm.UpstreamError
	if errors.As(err, &upstream) {
		switch upstream.StatusCode {
		case 408, 502, 504, 524:
			return true
		}
		return false
	}
	if isRetryableConversationMetadataRequestError(err) {
		return false
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.As(err, &network)
}

// Absence of AcceptedRequestError alone does not prove a request was not accepted.
// Only explicit overload rejections and connection establishment failures are replayed.
// Gateway/read timeouts (including 524), resets, malformed output and billing errors stop.
func isRetryableConversationMetadataRequestError(err error) bool {
	if err == nil || llm.RequestWasAccepted(err) {
		return false
	}
	var upstream *llm.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.StatusCode == 429 || upstream.StatusCode == 503
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns) && (dns.IsTimeout || dns.IsTemporary)
}

// Each field has a separate finite execution budget and retry count. Successful
// title/labels and charge settlement are never replayed to recover the other field.
func (s *Service) runConversationMetadataRecovery(parent context.Context, conversation model.Conversation, userMsg model.Message, options conversationMetadataRecoveryOptions) {
	setupCtx, cancel := background.WithTimeout(parent, options.attemptTimeout)
	plan := s.resolveConversationMetadataGenerationPlan(setupCtx, conversation)
	if !plan.shouldRun() {
		cancel()
		return
	}
	s.persistConversationFallbackTitle(setupCtx, conversation, userMsg)
	cancel()

	for _, field := range []string{"title", "labels"} {
		if (field == "title" && !plan.generateTitle) || (field == "labels" && !plan.generateLabels) {
			continue
		}
		var expectedTitle *string
		for attempt := 0; attempt < conversationMetadataRecoveryAttempts; attempt++ {
			attemptCtx, attemptCancel := background.WithTimeout(parent, options.attemptTimeout)
			current, err := s.repo.GetConversationByUser(attemptCtx, conversation.ID, conversation.UserID)
			if err != nil {
				attemptCancel()
				s.logConversationMetadataRecoveryStopped(conversation.ID, field, attempt+1, err)
				break
			}
			fieldPlan := conversationMetadataGenerationPlan{}
			if field == "title" {
				fallback := conversationTitleFromFirstUserMessage(userMsg.Content)
				if (!shouldAutoReplaceConversationTitle(current.Title) && current.Title != fallback) ||
					(expectedTitle != nil && current.Title != *expectedTitle) ||
					!s.autoGenerateConversationTitleEnabled(attemptCtx, conversation.UserID) {
					attemptCancel()
					break
				}
				if expectedTitle == nil {
					title := current.Title
					expectedTitle = &title
				}
				fieldPlan = conversationMetadataGenerationPlan{replaceTitle: true, generateTitle: true, expectedTitle: expectedTitle}
			} else {
				if !conversationLabelsEligibleForAutoGeneration(*current) || !s.autoGenerateConversationLabelsEnabled(attemptCtx, conversation.UserID) {
					attemptCancel()
					break
				}
				fieldPlan.generateLabels = true
			}
			fieldPlan.recoveryAttempt = &attempt
			// Keep the billed main model as the task-routing fallback, even if a later
			// conversation model edit occurs while this background task is running.
			current.Model = conversation.Model
			_, err = s.generateConversationMetadata(attemptCtx, *current, userMsg, fieldPlan)
			attemptCancel()
			if err == nil {
				break
			}
			var billingFailure *conversationMetadataBillingError
			if errors.As(err, &billingFailure) {
				s.logConversationMetadataRecoveryStopped(conversation.ID, field, attempt+1, err)
				return
			}
			var retryable *retryableConversationMetadataError
			if !errors.As(err, &retryable) || attempt == conversationMetadataRecoveryAttempts-1 {
				s.logConversationMetadataRecoveryStopped(conversation.ID, field, attempt+1, err)
				break
			}
			if s.logger != nil {
				s.logger.Info("conversation_metadata_retry_scheduled", zap.Uint("conversation_id", conversation.ID), zap.String("field", field), zap.Int("attempt", attempt+1), zap.Error(err))
			}
			waitCtx, waitCancel := background.WithTimeout(parent, 10*time.Second)
			err = options.wait(waitCtx, time.Second<<attempt)
			waitCancel()
			if err != nil {
				break
			}
		}
	}
}

func (s *Service) logConversationMetadataRecoveryStopped(conversationID uint, field string, attempts int, err error) {
	if s.logger != nil {
		s.logger.Warn("conversation_metadata_recovery_stopped", zap.Uint("conversation_id", conversationID), zap.String("field", field), zap.Int("attempts", attempts), zap.Error(err))
	}
}
