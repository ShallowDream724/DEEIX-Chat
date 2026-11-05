package conversation

import (
	"errors"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func routeMessageFileCount(cfg config.Config, route *channel.ResolvedRoute) int {
	if route == nil {
		return inputlimits.ResolveMessageFileCount(cfg.InputLimitsJSON, "", "", 0, cfg.MaxMessageFiles)
	}
	return inputlimits.ResolveMessageFileCount(cfg.InputLimitsJSON, llm.NormalizeAdapter(route.Protocol), route.BaseURL, route.UpstreamID, cfg.MaxMessageFiles)
}

// count is the number of original attachments in this turn, before any are
// converted to text or images for the provider request.
func validateRouteMessageFileCount(cfg config.Config, route *channel.ResolvedRoute, count int) error {
	limit := routeMessageFileCount(cfg, route)
	if limit > 0 && count > limit {
		return errors.Join(ErrTooManyMessageFiles, llm.NewInputLimitError("messageFileCount", int64(count), int64(limit)))
	}
	return nil
}

// Generated-file saves have no model route and use the system default.
func (s *Service) validateMessageFileCount(fileIDs []string) error {
	return validateRouteMessageFileCount(s.cfg.Snapshot(), nil, len(fileIDs))
}

// The intersection treats zero as an unbounded dimension.
func intersectChannelInputLimits(a, b inputlimits.Limits) inputlimits.Limits {
	return inputlimits.Limits{
		AttachmentCount:       lowerPositive(a.AttachmentCount, b.AttachmentCount),
		AttachmentBytes:       lowerPositive(a.AttachmentBytes, b.AttachmentBytes),
		AttachmentSingleBytes: lowerPositive(a.AttachmentSingleBytes, b.AttachmentSingleBytes),
		RequestBytes:          lowerPositive(a.RequestBytes, b.RequestBytes),
		ImageCount:            lowerPositive(a.ImageCount, b.ImageCount),
		ImageBytes:            lowerPositive(a.ImageBytes, b.ImageBytes),
		ImageSingleBytes:      lowerPositive(a.ImageSingleBytes, b.ImageSingleBytes),
		NativeCount:           lowerPositive(a.NativeCount, b.NativeCount),
		NativeBytes:           lowerPositive(a.NativeBytes, b.NativeBytes),
		PDFSingleBytes:        lowerPositive(a.PDFSingleBytes, b.PDFSingleBytes),
		PDFBytes:              lowerPositive(a.PDFBytes, b.PDFBytes),
		PDFPages:              lowerPositive(a.PDFPages, b.PDFPages),
		NonPDFBytes:           lowerPositive(a.NonPDFBytes, b.NonPDFBytes),
	}
}

func lowerPositive[T ~int | ~int64](a, b T) T {
	if a <= 0 || (b > 0 && b < a) {
		return b
	}
	return a
}
