package conversation

import (
	"context"
	"strings"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/traceid"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"go.uber.org/zap"
)

type sendPreparationStage int

const (
	sendPreparationClaim sendPreparationStage = iota
	sendPreparationAttachments
	sendPreparationRoute
	sendPreparationHistory
	sendPreparationNativeHydrate
	sendPreparationRetrieval
	sendPreparationPrompt
	sendPreparationTransport
	sendPreparationStageCount
)

var sendPreparationStageNames = [sendPreparationStageCount]string{
	"claim", "attachments", "route", "file_capability_history", "native_hydrate", "retrieval", "prompt", "transport",
}

// sendLatencyTiming is owned by the send goroutine, like the generation runner.
// It stores only durations and identifiers, never prompt or transport contents.
type sendLatencyTiming struct {
	logger       *zap.Logger
	fields       []zap.Field
	startedAt    time.Time
	stageStarted time.Time
	stage        sendPreparationStage
	durations    [sendPreparationStageCount]time.Duration
	prepared     bool
}

func newSendLatencyTiming(ctx context.Context, logger *zap.Logger, input SendMessageInput, runID string, startedAt time.Time) *sendLatencyTiming {
	return &sendLatencyTiming{
		logger: logger,
		fields: []zap.Field{
			zap.String("trace_id", traceid.FromContext(ctx)),
			zap.String("request_id", strings.TrimSpace(input.RequestID)),
			zap.String("run_id", runID),
			zap.Uint("conversation_id", input.ConversationID),
		},
		startedAt: startedAt, stageStarted: startedAt,
	}
}

func nonNegativeDuration(start, end time.Time) time.Duration {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

func (t *sendLatencyTiming) enter(stage sendPreparationStage, now time.Time) {
	if t == nil || t.prepared {
		return
	}
	t.durations[t.stage] += nonNegativeDuration(t.stageStarted, now)
	t.stage, t.stageStarted = stage, now
}

func (t *sendLatencyTiming) finishPreparation(now time.Time, failed bool) {
	if t == nil || t.prepared {
		return
	}
	t.enter(t.stage, now)
	t.prepared = true
	elapsed := nonNegativeDuration(t.startedAt, now)
	fields := append([]zap.Field{}, t.fields...)
	fields = append(fields, zap.Int64("prep_total_ms", elapsed.Milliseconds()), zap.Bool("failed", failed), zap.String("last_stage", sendPreparationStageNames[t.stage]))
	for stage, duration := range t.durations {
		fields = append(fields, zap.Int64(sendPreparationStageNames[stage]+"_ms", duration.Milliseconds()))
	}
	t.log("conversation_send_preparation", elapsed, failed, fields)
}

func (t *sendLatencyTiming) log(event string, elapsed time.Duration, failed bool, fields []zap.Field) {
	if t.logger == nil {
		return
	}
	if failed || elapsed >= time.Second {
		t.logger.Warn(event, fields...)
	} else {
		t.logger.Debug(event, fields...)
	}
}

// A separate observer is created for every actual upstream request, including
// the stream-to-nonstream fallback. Tool turns and retries keep distinct numbers.
type upstreamFirstEventTiming struct {
	send      *sendLatencyTiming
	startedAt time.Time
	attempt   int
	protocol  string
	stream    bool
	recorded  bool
}

func (t *sendLatencyTiming) beginUpstream(now time.Time, attempt int, protocol string, stream bool) *upstreamFirstEventTiming {
	if t == nil {
		return nil
	}
	t.finishPreparation(now, false)
	return &upstreamFirstEventTiming{send: t, startedAt: now, attempt: attempt, protocol: protocol, stream: stream}
}

func (t *upstreamFirstEventTiming) record(now time.Time, kind string, failed bool) {
	if t == nil || t.recorded {
		return
	}
	t.recorded = true
	wait := nonNegativeDuration(t.startedAt, now)
	fields := append([]zap.Field{}, t.send.fields...)
	fields = append(fields,
		zap.Int("llm_request_attempt", t.attempt), zap.String("protocol", t.protocol), zap.Bool("stream", t.stream),
		zap.String("first_event", kind), zap.Int64("wait_ms", wait.Milliseconds()),
		zap.Int64("elapsed_ms", nonNegativeDuration(t.send.startedAt, now).Milliseconds()), zap.Bool("failed", failed),
	)
	t.send.log("conversation_upstream_first_event", wait, failed, fields)
}

func (t *upstreamFirstEventTiming) recordResponse(now time.Time, output *llm.GenerateOutput, failed bool) {
	kind := "none"
	if output != nil {
		kind = "response"
	}
	t.record(now, kind, failed)
}

func streamEventTimingKind(event llm.GenerateStreamEvent) string {
	switch {
	case event.Reasoning != nil && event.Reasoning.Text != "":
		return "reasoning"
	case event.Delta != "":
		return "text"
	case event.ServerToolCall != nil:
		return "tool"
	case event.GeneratedImage != nil:
		return "image"
	case event.Usage != (llm.Usage{}):
		return "usage"
	default:
		return "metadata"
	}
}
