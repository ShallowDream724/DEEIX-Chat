package conversation

import (
	"context"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
)

type nativeRoutePreviewer interface {
	PreviewRoutes(context.Context, channel.ResolveRouteInput) ([]*channel.ResolvedRoute, error)
}

type NativeReadinessInput struct {
	UserID         uint
	Model          string
	FileIDs        []string
	ConversationID string
}

type NativeFileReadiness struct {
	FileID   string `json:"fileID"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

type NativeReadinessResult struct {
	Files []NativeFileReadiness `json:"files"`
}

// GetNativeReadiness is a read-only, conservative preview. Current files have
// priority over history in the shared selector, so historical files cannot take
// away any current file's budget between this preview and actual send.
func (s *Service) GetNativeReadiness(ctx context.Context, input NativeReadinessInput) (*NativeReadinessResult, error) {
	if err := s.validateMessageFileCount(input.FileIDs); err != nil {
		return nil, err
	}
	modelName := strings.TrimSpace(input.Model)
	if publicID := strings.TrimSpace(input.ConversationID); publicID != "" {
		conversation, err := s.GetConversationByPublicID(ctx, input.UserID, publicID)
		if err != nil {
			return nil, err
		}
		if modelName == "" {
			modelName = conversation.Model
		}
	}
	previewer, ok := s.routeResolver.(nativeRoutePreviewer)
	if !ok {
		return nil, ErrModelRouteNotConfigured
	}
	routes, err := previewer.PreviewRoutes(ctx, channel.ResolveRouteInput{
		PlatformModelName: modelName, TaskType: channel.TaskTypeChat, Scope: channel.RouteScopeUser, UserID: input.UserID,
	})
	if err != nil {
		return nil, mapRouteResolutionError(err)
	}
	attachments, err := s.resolveAttachments(ctx, input.UserID, input.FileIDs)
	if err != nil {
		return nil, err
	}
	for i := range attachments {
		attachments[i].Current = true
	}
	result := &NativeReadinessResult{Files: make([]NativeFileReadiness, len(attachments))}
	for i, att := range attachments {
		result.Files[i] = NativeFileReadiness{FileID: att.FileID, Eligible: len(routes) > 0}
	}
	for _, route := range routes {
		policy := resolveNativeInputPolicy(route)
		attachments = s.resolveNativePDFPageCounts(ctx, attachments, policy, nil)
		selected := make(map[string]bool)
		for _, att := range selectNativeInputs(attachments, policy) {
			selected[att.FileID] = true
		}
		for i, att := range attachments {
			if !policy.accepts(att) || !isNativeInputCandidate(att) {
				result.Files[i].Eligible = false
				result.Files[i].Reason = "unsupported"
			} else if !selected[att.FileID] {
				result.Files[i].Eligible = false
				result.Files[i].Reason = "budget_exceeded"
				if nativeInputKindOf(att) == nativeInputPDF && policy.budget().PDFPages > 0 && att.PageCount <= 0 {
					result.Files[i].Reason = "unavailable"
				}
			}
		}
	}
	// Check storage availability without reading or caching file contents. Actual
	// sends read the complete bounded bytes and validate again before generation.
	if s.storeProvider == nil {
		for i := range result.Files {
			result.Files[i].Eligible = false
			result.Files[i].Reason = "unavailable"
		}
		return result, nil
	}
	store, err := s.storeProvider.Open(ctx)
	if err != nil {
		for i := range result.Files {
			result.Files[i].Eligible = false
			result.Files[i].Reason = "unavailable"
		}
		return result, nil
	}
	for i, att := range attachments {
		if !result.Files[i].Eligible {
			continue
		}
		reader, info, openErr := store.Open(ctx, att.StoragePath)
		if openErr != nil {
			result.Files[i].Eligible = false
			result.Files[i].Reason = "unavailable"
			continue
		}
		_ = reader.Close()
		if info.SizeBytes != att.FileSize {
			result.Files[i].Eligible = false
			result.Files[i].Reason = "unavailable"
		}
	}
	return result, nil
}

func (s *Service) validateMessageFileCount(fileIDs []string) error {
	maxFiles := s.cfg.Snapshot().MaxMessageFiles
	if maxFiles <= 0 {
		maxFiles = 10
	}
	if len(fileIDs) > maxFiles {
		return ErrTooManyMessageFiles
	}
	return nil
}
