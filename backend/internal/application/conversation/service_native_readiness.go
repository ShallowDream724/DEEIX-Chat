package conversation

import (
	"context"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

type nativeRoutePreviewer interface {
	PreviewRoutes(context.Context, channel.ResolveRouteInput) ([]*channel.ResolvedRoute, error)
}

type NativeReadinessInput struct {
	UserID                uint
	Model                 string
	FileIDs               []string
	ConversationID        string
	ParentMessagePublicID string
	SelectedToolIDs       []uint
}

type NativeFileReadiness struct {
	FileID   string
	Eligible bool
	Reason   string
}

type AttachmentReadinessIssue struct {
	Dimension    string
	Actual       int64
	Limit        int64
	FileName     string
	FileCategory string
	Scope        string
}

type NativeReadinessResult struct {
	Files            []NativeFileReadiness
	MessageFileCount int
	Limits           inputlimits.Limits
	Issues           []AttachmentReadinessIssue
}

// GetNativeReadiness checks route policy against owned file metadata. It never
// reads attachment contents, resolves a billing route, or waits for extraction.
func (s *Service) GetNativeReadiness(ctx context.Context, input NativeReadinessInput) (*NativeReadinessResult, error) {
	modelName := strings.TrimSpace(input.Model)
	var conversationID uint
	if publicID := strings.TrimSpace(input.ConversationID); publicID != "" {
		conversation, err := s.GetConversationByPublicID(ctx, input.UserID, publicID)
		if err != nil {
			return nil, err
		}
		conversationID = conversation.ID
		if modelName == "" {
			modelName = conversation.Model
		}
	}
	previewer, ok := s.routeResolver.(nativeRoutePreviewer)
	if !ok {
		return nil, ErrModelRouteNotConfigured
	}
	routes, err := previewer.PreviewRoutes(ctx, channel.ResolveRouteInput{
		PlatformModelName: modelName, TaskType: channel.TaskTypeChat, Scope: channel.RouteScopeUser,
		UserID: input.UserID, ConversationID: conversationID,
	})
	if err != nil {
		return nil, mapRouteResolutionError(err)
	}
	if len(routes) == 0 {
		return nil, ErrModelRouteNotConfigured
	}

	var history []model.Message
	if parentID := strings.TrimSpace(input.ParentMessagePublicID); parentID != "" {
		if conversationID == 0 {
			return nil, ErrInvalidMessageBranch
		}
		parent, err := s.repo.GetMessageByPublicID(ctx, conversationID, input.UserID, parentID)
		if err != nil || parent == nil || parent.ID == 0 {
			return nil, ErrInvalidMessageBranch
		}
		branch := &messageBranchState{ParentMessageID: &parent.ID}
		if err := s.loadMessageBranchContext(ctx, conversationID, branch, nil); err != nil {
			return nil, err
		}
		history = filterDefaultBranchContextMessages(filterBlockedMessages(recoverAssistantRetryUserStates(branch.ExistingMessages)), 0)
	}

	fileIDs := collectConversationFileIDs(history, input.FileIDs)
	attachments, err := s.resolveConversationFileContext(ctx, input.UserID, fileIDs, input.FileIDs)
	if err != nil {
		return nil, err
	}
	attachments = bindAttachmentMessageRoles(attachments, history)
	current := filterCurrentAttachments(attachments)
	result := &NativeReadinessResult{
		Files:  make([]NativeFileReadiness, len(current)),
		Issues: make([]AttachmentReadinessIssue, 0),
	}
	for i, att := range current {
		result.Files[i] = NativeFileReadiness{FileID: att.FileID, Eligible: true}
	}

	cfg := s.cfg.Snapshot()
	var canUseRAG bool
	if len(attachments) > 0 && s.ragSvc != nil && s.embeddingSvc != nil && cfg.RAGEnabled && cfg.EmbeddingEnabled {
		fileMode, err := s.getUserSettingCached(ctx, input.UserID, "chat.file_mode")
		if err != nil || fileMode != "full_context" {
			canUseRAG, _ = s.embeddingSvc.Available(ctx)
		}
	}
	currentCount := len(current)
	for i, route := range routes {
		policy := resolveNativeInputPolicy(route, cfg.InputLimitsJSON)
		policy.HTTPS = promptPolicyForRoute(route).OriginalAttachments && s.attachmentHTTPSAvailable()
		usesImageProcessor := false
		if len(current) > 0 && len(input.SelectedToolIDs) > 0 && promptPolicyForRoute(route).LocalTools {
			tools, err := s.resolveRouteToolRuntime(ctx, route, input.SelectedToolIDs)
			usesImageProcessor = err == nil && tools.attachmentProcessor != nil
		}
		count := routeMessageFileCount(cfg, route)
		if i == 0 {
			result.Limits, result.MessageFileCount = policy.InputLimits, count
		} else {
			result.Limits = intersectChannelInputLimits(result.Limits, policy.InputLimits)
			if result.MessageFileCount == 0 || count == 0 {
				result.MessageFileCount = 0
			} else {
				result.MessageFileCount = max(result.MessageFileCount, count)
			}
		}
		if len(attachments) == 0 {
			continue
		}
		reasons, routeIssues := previewRouteAttachmentMetadata(attachments, policy, usesImageProcessor, canUseRAG)
		if count > 0 && currentCount > count {
			routeIssues = append([]AttachmentReadinessIssue{{
				Dimension: "messageFileCount", Actual: int64(currentCount), Limit: int64(count), Scope: "current",
			}}, routeIssues...)
		}
		for index, file := range result.Files {
			if reason := reasons[file.FileID]; reason != "" && (file.Eligible || nativeReadinessReasonPriority(reason) > nativeReadinessReasonPriority(file.Reason)) {
				result.Files[index].Eligible, result.Files[index].Reason = false, reason
			}
		}
		result.Issues = intersectReadinessIssues(result.Issues, routeIssues, i == 0)
	}
	return result, nil
}

// These aggregates are lower bounds: converted images, repeated parts, text,
// retrieval results and protocol framing are accounted for during the send.
func previewRouteAttachmentMetadata(attachments []AttachmentInput, policy nativeInputPolicy, usesImageProcessor, canUseRAG bool) (map[string]string, []AttachmentReadinessIssue) {
	reasons := make(map[string]string, len(attachments))
	issues := make([]AttachmentReadinessIssue, 0)
	fileIssue := func(att AttachmentInput, dimension string, actual, limit int64) {
		scope := "history"
		if att.Current {
			scope = "current"
		}
		issues = append(issues, AttachmentReadinessIssue{
			Dimension: dimension, Actual: actual, Limit: limit, FileName: att.FileName,
			FileCategory: readinessFileCategory(att), Scope: scope,
		})
	}
	var imageCount, nativeCount, nativeBytes, pdfBytes, pdfPages, nonPDFBytes, requestBytes readinessMeasure
	var attachmentCount, attachmentBytes readinessMeasure
	var images, natives []AttachmentInput
	for _, att := range attachments {
		if !isUserSubmittedAttachment(att) {
			continue
		}
		image := readinessFileCategory(att) == "image"
		switch {
		case image:
			if strings.TrimSpace(att.StoragePath) == "" || att.FileSize <= 0 {
				reasons[att.FileID] = "unavailable"
				fileIssue(att, "unavailable", 0, 0)
				continue
			}
			if policy.ImageUnsupported {
				reasons[att.FileID] = "unsupported"
				if !att.Current || !usesImageProcessor {
					fileIssue(att, "unsupported", 0, 0)
				}
			} else if !att.Current || !usesImageProcessor {
				images = append(images, att)
				imageCount.add(att, 1)
				attachmentCount.add(att, 1)
			}
		case policy.accepts(att) && isNativeInputCandidate(att):
			natives = append(natives, att)
			attachmentCount.add(att, 1)
			attachmentBytes.add(att, att.FileSize)
			if limit := policy.InputLimits.AttachmentSingleBytes; limit > 0 && att.FileSize > limit {
				fileIssue(att, "attachmentSingleBytes", att.FileSize, limit)
				reasons[att.FileID] = "budget_exceeded"
			}
			nativeCount.add(att, 1)
			nativeBytes.add(att, att.FileSize)
			requestBytes.add(att, ((att.FileSize+2)/3)*4)
			if nativeInputKindOf(att) == nativeInputPDF {
				pdfBytes.add(att, att.FileSize)
				if att.PageCount > 0 {
					pdfPages.add(att, int64(att.PageCount))
				} else if policy.InputLimits.PDFPages > 0 {
					// No metadata for this page count. Keep waiting for
					// ordinary readiness; the send path can inspect the PDF.
					reasons[att.FileID] = "unavailable"
				}
				if limit := policy.InputLimits.PDFSingleBytes; limit > 0 && att.FileSize > limit {
					fileIssue(att, "pdfSingleBytes", att.FileSize, limit)
					reasons[att.FileID] = "budget_exceeded"
				}
			} else {
				nonPDFBytes.add(att, att.FileSize)
			}
		case policy.accepts(att):
			reasons[att.FileID] = "unavailable"
			fileIssue(att, "unavailable", 0, 0)
		default:
			reasons[att.FileID] = "unsupported"
			if isUserMediaAttachment(att) {
				fileIssue(att, "unsupported", 0, 0)
			} else if attachmentExtractionUnavailable(att) && !canRetrieveAttachment(att, canUseRAG) {
				fileIssue(att, "unavailable", 0, 0)
			}
		}
	}
	limits := policy.InputLimits
	for _, item := range []struct {
		dimension string
		measure   readinessMeasure
		limit     int64
		affected  []AttachmentInput
	}{
		{"imageCount", imageCount, int64(limits.ImageCount), images},
		{"attachmentCount", attachmentCount, int64(limits.AttachmentCount), append(append([]AttachmentInput(nil), images...), natives...)},
		{"attachmentBytes", attachmentBytes, limits.AttachmentBytes, natives},
		{"nativeCount", nativeCount, int64(limits.NativeCount), natives},
		{"nativeBytes", nativeBytes, limits.NativeBytes, natives},
		{"pdfBytes", pdfBytes, limits.PDFBytes, natives},
		{"pdfPages", pdfPages, int64(limits.PDFPages), natives},
		{"nonPDFBytes", nonPDFBytes, limits.NonPDFBytes, natives},
		{"requestBytes", requestBytes, inlineRequestByteLimit(policy), natives},
	} {
		if actual := item.measure.current + item.measure.history; item.limit > 0 && actual > item.limit {
			issues = append(issues, AttachmentReadinessIssue{Dimension: item.dimension, Actual: actual, Limit: item.limit, Scope: item.measure.scope()})
			for _, att := range item.affected {
				reasons[att.FileID] = "budget_exceeded"
			}
		}
	}
	return reasons, issues
}

type readinessMeasure struct {
	current, history int64
}

func (m *readinessMeasure) add(att AttachmentInput, amount int64) {
	if att.Current {
		m.current += amount
	} else {
		m.history += amount
	}
}

func (m readinessMeasure) scope() string {
	if m.history == 0 {
		return "current"
	}
	if m.current == 0 {
		return "history"
	}
	return "context"
}

func readinessFileCategory(att AttachmentInput) string {
	if normalizeAttachmentKind(att.Kind, att.DetectedMIME) == "image" || att.FileCategory == "image" {
		return "image"
	}
	if kind := nativeInputKindOf(att); kind != "" {
		return string(kind)
	}
	if att.FileCategory == "document" {
		return "document"
	}
	return "other"
}

func attachmentExtractionUnavailable(att AttachmentInput) bool {
	if att.ExtractStatus == "ready" {
		return false
	}
	return stringsEqualFold(att.ProcessingStatus, "failed") || stringsEqualFold(att.ExtractStatus, "failed") ||
		stringsEqualFold(att.ExtractStatus, model.FileSubprocessStatusEmpty)
}

func nativeReadinessReasonPriority(reason string) int {
	switch reason {
	case "unsupported":
		return 3
	case "budget_exceeded":
		return 2
	case "unavailable":
		return 1
	default:
		return 0
	}
}

// A blocking issue is safe to return only if every candidate identifies it
// with the same measurement. The actual route decides other violations.
func intersectReadinessIssues(common, route []AttachmentReadinessIssue, first bool) []AttachmentReadinessIssue {
	if !first && len(common) == 0 {
		return common
	}
	byIssue := make(map[AttachmentReadinessIssue]struct{}, len(route))
	if first {
		for _, issue := range route {
			if _, seen := byIssue[issue]; !seen {
				byIssue[issue] = struct{}{}
				common = append(common, issue)
			}
		}
		return common
	}
	for _, issue := range route {
		byIssue[issue] = struct{}{}
	}
	result := common[:0]
	for _, issue := range common {
		if _, found := byIssue[issue]; found {
			result = append(result, issue)
		}
	}
	return result
}
