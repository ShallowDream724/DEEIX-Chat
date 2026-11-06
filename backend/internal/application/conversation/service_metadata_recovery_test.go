package conversation

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type metadataRecoveryRepo struct {
	repository.ConversationRepository
	current  model.Conversation
	writeErr error
}

func (r *metadataRecoveryRepo) GetUserSettingValue(context.Context, uint, string) (string, error) {
	return "true", nil
}
func (r *metadataRecoveryRepo) GetConversationByUser(context.Context, uint, uint) (*model.Conversation, error) {
	current := r.current
	return &current, nil
}
func (r *metadataRecoveryRepo) UpdateConversationMetadata(_ context.Context, _ uint, patch repository.ConversationMetadataPatch) (*model.Conversation, error) {
	if r.writeErr != nil {
		return nil, r.writeErr
	}
	if patch.ExpectedTitle == nil || r.current.Title == *patch.ExpectedTitle {
		eligible := shouldAutoReplaceConversationTitle(r.current.Title)
		for _, title := range patch.ReplaceableTitles {
			eligible = eligible || r.current.Title == title
		}
		if eligible {
			r.current.Title = patch.Title
			r.current.UpdatedAt = r.current.UpdatedAt.Add(time.Second)
		}
	}
	current := r.current
	return &current, nil
}
func (r *metadataRecoveryRepo) SetGeneratedConversationLabelsIfEligible(_ context.Context, _ uint, labels string) (*model.Conversation, bool, error) {
	applied := conversationLabelsEligibleForAutoGeneration(r.current)
	if applied {
		r.current.LabelsJSON = labels
		r.current.UpdatedAt = r.current.UpdatedAt.Add(time.Second)
	}
	current := r.current
	return &current, applied, nil
}

type metadataRecoveryGateway struct {
	llmGateway
	titles, labels int
	routeModels    []string
	generate       func(context.Context, string, int) (*llm.GenerateOutput, error)
}

func (g *metadataRecoveryGateway) Generate(ctx context.Context, route llm.RouteConfig, input llm.GenerateInput) (*llm.GenerateOutput, error) {
	g.routeModels = append(g.routeModels, route.UpstreamModel)
	field, attempt := "title", 0
	if strings.Contains(input.Messages[0].Content, "topic labels") {
		field = "labels"
		g.labels++
		attempt = g.labels
	} else {
		g.titles++
		attempt = g.titles
	}
	if g.generate != nil {
		return g.generate(ctx, field, attempt)
	}
	return metadataRecoveryOutput(field), nil
}
func metadataRecoveryOutput(field string) *llm.GenerateOutput {
	if field == "title" {
		return &llm.GenerateOutput{Text: `{"title":"Generated title"}`}
	}
	return &llm.GenerateOutput{Text: `{"labels":["technology"]}`}
}
func newMetadataRecoveryService() (*Service, *metadataRecoveryRepo, *metadataRecoveryGateway) {
	r := &metadataRecoveryRepo{current: model.Conversation{ID: 1, UserID: 2, Title: "新对话", Model: "test", LabelsJSON: "[]", UpdatedAt: time.Unix(1, 0)}}
	g := &metadataRecoveryGateway{}
	s := &Service{cfg: config.NewRuntime(config.Config{ConversationTaskModel: "test"}), repo: r, llmClient: g,
		routeResolver: &textTaskRouteResolverStub{routes: map[string]*channel.ResolvedRoute{"test": {PlatformModelName: "test", Protocol: llm.AdapterOpenAIChatCompletions}}}}
	return s, r, g
}
func runMetadataRecoveryForTest(s *Service, conversation model.Conversation, wait func(context.Context, time.Duration) error) {
	if wait == nil {
		wait = func(context.Context, time.Duration) error { return nil }
	}
	s.runConversationMetadataRecovery(context.Background(), conversation, model.Message{ID: 3, RunID: "run", Content: "first user question"}, conversationMetadataRecoveryOptions{attemptTimeout: time.Second, wait: wait})
}

func TestConversationMetadataRecoveryRetriesOnlyFailedField(t *testing.T) {
	for _, failedField := range []string{"title", "labels"} {
		t.Run(failedField, func(t *testing.T) {
			s, r, g := newMetadataRecoveryService()
			g.generate = func(_ context.Context, field string, attempt int) (*llm.GenerateOutput, error) {
				if field == failedField && attempt == 1 {
					return nil, &llm.UpstreamError{StatusCode: 503}
				}
				return metadataRecoveryOutput(field), nil
			}
			runMetadataRecoveryForTest(s, r.current, nil)
			wantTitles, wantLabels := 1, 1
			if failedField == "title" {
				wantTitles = 2
			} else {
				wantLabels = 2
			}
			if g.titles != wantTitles || g.labels != wantLabels || r.current.Title != "Generated title" || r.current.LabelsJSON != `["technology"]` {
				t.Fatalf("calls title=%d labels=%d current=%+v", g.titles, g.labels, r.current)
			}
		})
	}
}

func TestConversationMetadataRecoveryStopsPermanentAndUnknownErrors(t *testing.T) {
	for name, cause := range map[string]error{"auth": &llm.UpstreamError{StatusCode: 401}, "bad_input": &llm.UpstreamError{StatusCode: 400}, "gateway_timeout": &llm.UpstreamError{StatusCode: 524}, "accepted": llm.MarkRequestAccepted(context.DeadlineExceeded), "unknown_timeout": context.DeadlineExceeded} {
		t.Run(name, func(t *testing.T) {
			s, r, g := newMetadataRecoveryService()
			g.generate = func(_ context.Context, field string, _ int) (*llm.GenerateOutput, error) {
				if field == "title" {
					return nil, cause
				}
				return metadataRecoveryOutput(field), nil
			}
			runMetadataRecoveryForTest(s, r.current, nil)
			if g.titles != 1 || g.labels != 1 {
				t.Fatalf("calls title=%d labels=%d", g.titles, g.labels)
			}
		})
	}
}

func TestConversationMetadataRecoveryHasFiniteAttempts(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	g.generate = func(context.Context, string, int) (*llm.GenerateOutput, error) {
		return nil, &llm.UpstreamError{StatusCode: 429}
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 3 || g.labels != 3 {
		t.Fatalf("calls title=%d labels=%d", g.titles, g.labels)
	}
}

func TestConversationMetadataRecoveryContextsAreIndependent(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), "metadata-test", "retained"))
	cancel()
	var titleCtx context.Context
	g.generate = func(ctx context.Context, field string, _ int) (*llm.GenerateOutput, error) {
		if ctx.Err() != nil || ctx.Value("metadata-test") != "retained" {
			t.Fatalf("lost detached context: %v", ctx.Err())
		}
		if field == "title" {
			titleCtx = ctx
			return nil, context.DeadlineExceeded
		}
		if titleCtx == nil || titleCtx.Err() == nil {
			t.Fatal("previous field context must have been canceled")
		}
		return metadataRecoveryOutput(field), nil
	}
	s.runConversationMetadataRecovery(parent, r.current, model.Message{Content: "first user question"}, conversationMetadataRecoveryOptions{attemptTimeout: time.Second, wait: func(context.Context, time.Duration) error { return nil }})
	if g.titles != 1 || g.labels != 1 {
		t.Fatalf("calls title=%d labels=%d", g.titles, g.labels)
	}
}

func TestConversationMetadataRecoveryManualRenameWins(t *testing.T) {
	for _, duringRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "during_retry", true: "inflight_rename"}[duringRequest], func(t *testing.T) {
			s, r, g := newMetadataRecoveryService()
			g.generate = func(_ context.Context, field string, _ int) (*llm.GenerateOutput, error) {
				if field == "title" {
					if !duringRequest {
						return nil, &llm.UpstreamError{StatusCode: 503}
					}
					r.current.Title = "Manual title"
				}
				return metadataRecoveryOutput(field), nil
			}
			runMetadataRecoveryForTest(s, r.current, func(context.Context, time.Duration) error {
				r.current.Title = "Manual title"
				r.current.UpdatedAt = r.current.UpdatedAt.Add(time.Second)
				return nil
			})
			if g.titles != 1 || r.current.Title == "Generated title" {
				t.Fatalf("manual edit lost: calls=%d title=%q", g.titles, r.current.Title)
			}
		})
	}
}

func TestConversationMetadataRecoveryPersistenceFailureDoesNotRegenerate(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	g.generate = func(_ context.Context, field string, _ int) (*llm.GenerateOutput, error) {
		if field == "title" {
			r.writeErr = &llm.UpstreamError{StatusCode: 503}
		}
		return metadataRecoveryOutput(field), nil
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 1 {
		t.Fatalf("generated %d titles for persistence failure", g.titles)
	}
}

func TestConversationMetadataRecoveryConcurrentConversationWritePreservesGeneratedTitle(t *testing.T) {
	for _, change := range []string{"continue_chat", "model", "star"} {
		for _, duringRetry := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/inflight", true: "/retry"}[duringRetry], func(t *testing.T) {
				s, r, g := newMetadataRecoveryService()
				mutate := func() {
					switch change {
					case "continue_chat":
						r.current.MessageCount++
					case "model":
						r.current.Model = "changed-model"
					case "star":
						r.current.IsStarred = true
					}
					r.current.UpdatedAt = r.current.UpdatedAt.Add(time.Second)
				}
				g.generate = func(_ context.Context, field string, attempt int) (*llm.GenerateOutput, error) {
					if field == "title" && attempt == 1 {
						if duringRetry {
							return nil, &llm.UpstreamError{StatusCode: 503}
						}
						mutate()
					}
					return metadataRecoveryOutput(field), nil
				}
				runMetadataRecoveryForTest(s, r.current, func(context.Context, time.Duration) error { mutate(); return nil })
				wantCalls := 1
				if duringRetry {
					wantCalls = 2
				}
				if g.titles != wantCalls || r.current.Title != "Generated title" {
					t.Fatalf("calls=%d title=%q", g.titles, r.current.Title)
				}
			})
		}
	}
}

func configureMetadataRecoveryFallback(s *Service) {
	s.cfg = config.NewRuntime(config.Config{ConversationTaskModel: textTaskFollowModel})
	resolver := s.routeResolver.(*textTaskRouteResolverStub)
	resolver.routes["test"].UpstreamModel = "current"
	resolver.defaultRoute = &channel.ResolvedRoute{PlatformModelName: "default", Protocol: llm.AdapterOpenAIChatCompletions, UpstreamModel: "default"}
}

func TestConversationMetadataRecoveryAdvancesToHealthyFallbackWithinBudget(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	configureMetadataRecoveryFallback(s)
	g.generate = func(_ context.Context, field string, attempt int) (*llm.GenerateOutput, error) {
		if field == "title" && attempt == 1 {
			return nil, &llm.UpstreamError{StatusCode: 503}
		}
		return metadataRecoveryOutput(field), nil
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 2 || len(g.routeModels) != 3 || g.routeModels[0] != "current" || g.routeModels[1] != "default" || r.current.Title != "Generated title" {
		t.Fatalf("routes=%v title=%q", g.routeModels, r.current.Title)
	}
}

func TestConversationMetadataRecoveryFallbackDoesNotMultiplyCallBudget(t *testing.T) {
	s, r, g := newMetadataRecoveryService()
	configureMetadataRecoveryFallback(s)
	g.generate = func(context.Context, string, int) (*llm.GenerateOutput, error) {
		return nil, &llm.UpstreamError{StatusCode: 503}
	}
	runMetadataRecoveryForTest(s, r.current, nil)
	if g.titles != 3 || g.labels != 3 {
		t.Fatalf("calls title=%d labels=%d routes=%v", g.titles, g.labels, g.routeModels)
	}
}

func TestConversationMetadataManualCallRetainsSafeCandidateFallback(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known_rejection", true: "unknown_result"}[unknown], func(t *testing.T) {
			s, _, g := newMetadataRecoveryService()
			configureMetadataRecoveryFallback(s)
			g.generate = func(_ context.Context, field string, attempt int) (*llm.GenerateOutput, error) {
				if attempt == 1 {
					if unknown {
						return nil, llm.MarkRequestAccepted(context.DeadlineExceeded)
					}
					return nil, &llm.UpstreamError{StatusCode: 503}
				}
				return metadataRecoveryOutput(field), nil
			}
			_, err := s.callConversationMetadataLLM(context.Background(), conversationMetadataLLMInput{ConfiguredModel: textTaskFollowModel, ConversationModel: "test", ServiceCode: "title", Prompt: "title"})
			if unknown {
				if err == nil || g.titles != 1 {
					t.Fatalf("unknown request replayed: calls=%d err=%v", g.titles, err)
				}
			} else if err != nil || g.titles != 2 || g.routeModels[1] != "default" {
				t.Fatalf("safe fallback failed: routes=%v err=%v", g.routeModels, err)
			}
		})
	}
}

func TestConversationMetadataRecoveryManualLabelsWin(t *testing.T) {
	for _, duringRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "during_retry", true: "inflight_clear"}[duringRequest], func(t *testing.T) {
			s, r, g := newMetadataRecoveryService()
			g.generate = func(_ context.Context, field string, _ int) (*llm.GenerateOutput, error) {
				if field == "labels" {
					if !duringRequest {
						return nil, &llm.UpstreamError{StatusCode: 503}
					}
					r.current.LabelsManuallyManaged = true
				}
				return metadataRecoveryOutput(field), nil
			}
			runMetadataRecoveryForTest(s, r.current, func(context.Context, time.Duration) error { r.current.LabelsManuallyManaged = true; return nil })
			if g.labels != 1 || r.current.LabelsJSON != "[]" {
				t.Fatalf("manual labels overwritten: calls=%d labels=%s", g.labels, r.current.LabelsJSON)
			}
		})
	}
}

func TestConversationMetadataRecoveryRegistrySuppressesDuplicates(t *testing.T) {
	var registry conversationMetadataRecoveryRegistry
	key := conversationMetadataRecoveryKey{1, 2, "run"}
	var wg sync.WaitGroup
	results := make(chan bool, 20)
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- registry.start(key) }()
	}
	wg.Wait()
	close(results)
	owners := 0
	for won := range results {
		if won {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners=%d", owners)
	}
	registry.finish(key)
	if registry.start(key) {
		t.Fatal("completed post-billing replay started again")
	}
}

func TestConversationMetadataRetryClassification(t *testing.T) {
	for name, cause := range map[string]error{"deadline": context.DeadlineExceeded, "read": &net.OpError{Op: "read", Err: io.ErrUnexpectedEOF}, "accepted_503": llm.MarkRequestAccepted(&llm.UpstreamError{StatusCode: 503}), "config": errors.New("model missing")} {
		if isRetryableConversationMetadataRequestError(cause) {
			t.Errorf("%s must not be replayed", name)
		}
	}
	if !isRetryableConversationMetadataRequestError(&net.OpError{Op: "dial", Err: errors.New("connection refused")}) {
		t.Fatal("dial failure should be recoverable")
	}
}
