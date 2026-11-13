package conversation

import (
	"context"
	"sort"
	"strings"

	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

// TemporaryChatAttachmentLimit resolves the request-wide ephemeral attachment policy.
func TemporaryChatAttachmentLimit(cfg config.Config) int {
	if cfg.TemporaryChatMaxAttachments > 0 {
		return cfg.TemporaryChatMaxAttachments
	}
	return 20
}

// TemporaryChatRequestLimit bounds JSON context and multipart metadata, excluding file bytes.
func TemporaryChatRequestLimit(cfg config.Config) int64 {
	if cfg.TemporaryChatRequestMaxBytes > 0 {
		return cfg.TemporaryChatRequestMaxBytes
	}
	return 8 << 20
}

const (
	fileCategoryImage        = "image"
	fileCategoryVideo        = "video"
	fileCategoryAudio        = "audio"
	fileCategoryPDF          = "pdf"
	fileCategoryWord         = "word"
	fileCategoryPresentation = "presentation"
	fileCategoryExcel        = "excel"
	fileCategoryText         = "text"
	fileCategoryUnknown      = "unknown"
)

func parseAllowedMIMETypes(raw string) map[string]struct{} {
	items := strings.Split(raw, ",")
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		value := strings.ToLower(strings.TrimSpace(item))
		if value == "" {
			continue
		}
		result[value] = struct{}{}
	}
	return result
}

type chatFileCapability struct {
	RAGAvailable           bool
	RAGAvailabilityReason  string
	CapabilityMode         string
	EffectiveImageMaxBytes int64
	EffectiveDocMaxBytes   int64
	EffectiveAudioMaxBytes int64
	EffectiveVideoMaxBytes int64
}

func sortedAllowedMIMETypes(raw string) []string {
	result := make([]string, 0)
	for value := range parseAllowedMIMETypes(raw) {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// resolveMessageFilePolicy waits until the active prompt's file set is known.
// Plain text turns have no use for file-mode preferences or vector-store readiness.
// Historical attachments and enabled knowledge-base references keep the full checks.
func (s *Service) resolveMessageFilePolicy(ctx context.Context, userID uint, conversationFileIDs, knowledgeBaseIDs []string, cfg config.Config) (string, chatFileCapability) {
	fileMode := "auto"
	if len(conversationFileIDs) == 0 && !(cfg.KnowledgeBaseEnabled && len(knowledgeBaseIDs) > 0) {
		return fileMode, chatFileCapability{}
	}
	capability := s.resolveChatFileCapability(ctx)
	if value, err := s.getUserSettingCached(ctx, userID, "chat.file_mode"); err == nil && value != "" {
		fileMode = value
	}
	return fileMode, capability
}

func (s *Service) resolveChatFileCapability(ctx context.Context) chatFileCapability {
	cfg := s.cfg.Snapshot()
	capability := chatFileCapability{
		EffectiveImageMaxBytes: appupload.EffectiveMaxBytes(cfg, "image/png", ""),
		EffectiveDocMaxBytes:   appupload.EffectiveMaxBytes(cfg, "text/plain", ""),
		EffectiveAudioMaxBytes: appupload.EffectiveMaxBytes(cfg, "audio/mpeg", ""),
		EffectiveVideoMaxBytes: appupload.EffectiveMaxBytes(cfg, "video/mp4", ""),
	}

	ragAvailable, reason := s.embeddingSvc.Available(ctx)
	capability.RAGAvailable = ragAvailable
	capability.RAGAvailabilityReason = reason
	capability.CapabilityMode = "full_context_and_rag"
	if !ragAvailable {
		capability.CapabilityMode = "full_context_only"
	}
	return capability
}
