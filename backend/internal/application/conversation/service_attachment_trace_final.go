package conversation

import (
	"context"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// resolveFinal reconciles planned file access with the prompt that survived the
// complete request token budget. File names are display metadata, never identity.
func (t *attachmentRouteTrace) resolveFinal(ctx context.Context, s *Service, route *channel.ResolvedRoute, attachments []AttachmentInput, cache *nativeInputCache, messages []llm.Message, cfg config.Config) {
	native := s.resolveNativeInputs(ctx, route, attachments, cache, cfg)
	items := routeAttachmentTraceItems(t.items, native, t.skipImages)
	items = attachmentTraceItemsForMessages(items, messages)
	t.summary, t.markdown, t.payload = buildAttachmentProcessTrace(t.fileMode, items)
	t.annotateExclusionReasons(native)
}

func (t *attachmentRouteTrace) recordPlannedMessages(messages []llm.Message) {
	t.plannedFileIDs = messageSourceFileIDs(messages)
}

func messageSourceFileIDs(messages []llm.Message) map[string]struct{} {
	included := make(map[string]struct{})
	for _, message := range messages {
		for _, part := range message.Parts {
			for _, raw := range part.SourceFileIDs {
				if id := strings.TrimSpace(raw); id != "" {
					included[id] = struct{}{}
				}
			}
		}
	}
	return included
}

func (t *attachmentRouteTrace) annotateExclusionReasons(native nativeInputs) {
	if t.payload == nil {
		return
	}
	original := make(map[string]AttachmentInput, len(t.items))
	for _, item := range t.items {
		original[strings.TrimSpace(item.FileID)] = item
	}
	annotate := func(refs []attachmentTraceFileRef) {
		for i := range refs {
			ref := &refs[i]
			if ref.ContextMode != fileContextModeSkipped && ref.ContextMode != fileContextModeUnsupported {
				continue
			}
			id := strings.TrimSpace(ref.FileID)
			switch {
			case ref.ContextMode == fileContextModeUnsupported:
				ref.Reason = "unsupported"
			case hasPlannedFile(t.plannedFileIDs, id):
				ref.Reason = "context_budget"
			case native.Rejected[id] != nil:
				ref.Reason, ref.Limit = "input_budget", llm.InputLimitDetails(native.Rejected[id])
			case native.Selected[id] && native.Parts[id].Kind == "":
				ref.Reason = "read_failed"
			default:
				if original[id].ContextMode == fileContextModeRAG {
					ref.Reason = "no_retrieved_content"
				} else if strings.TrimSpace(original[id].ExtractedText) == "" && original[id].ContextMode == fileContextModeSkipped {
					ref.Reason = "content_unavailable"
				}
			}
		}
	}
	annotate(t.payload.FileRefs)
	if groups := t.payload.FileGroupRefs; groups != nil {
		annotate(groups.Skipped)
		annotate(groups.Unsupported)
	}
}

func hasPlannedFile(ids map[string]struct{}, id string) bool {
	_, ok := ids[id]
	return ok
}

func attachmentTraceItemsForMessages(items []AttachmentInput, messages []llm.Message) []AttachmentInput {
	included := messageSourceFileIDs(messages)
	result := append([]AttachmentInput(nil), items...)
	for i := range result {
		if _, ok := included[strings.TrimSpace(result[i].FileID)]; ok {
			continue
		}
		if result[i].ContextMode != fileContextModeUnsupported {
			result[i].ContextMode = fileContextModeSkipped
		}
	}
	return result
}
