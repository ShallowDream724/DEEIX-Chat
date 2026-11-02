package conversation

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/textutil"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"go.uber.org/zap"
)

// 原生输入策略
//
// 文件能否以原生内容块发送，由四个条件同时决定：
//  1. 模型输入模态包含该类型：管理员在能力 JSON 中显式声明 inputModalities，否则取 models.dev 目录；
//  2. 路由协议能编码该类型与格式（见 nativeProtocolSupport）；
//  3. 上游可信：原生协议（Anthropic Messages、Gemini）按原样透传；
//     OpenAI 兼容协议只在官方地址或显式声明时启用，第三方中转站可能不认识这些内容块；
//  4. 大小、页数与数量在保守上限内。
//
// 文件一律以 base64 内嵌在请求体中，不使用各家 Files API。PDF 不满足条件时回退到既有的文本提取；
// 音频与视频没有文本可回退，本轮不支持时阻止生成，历史轮次保留「不支持」说明。
// 策略按路由计算：故障转移到另一条路由时会重新判断，不会把原生内容块发给不支持它的协议。

// 未单独声明的协议继续使用原有保守预算。官方 OpenAI 与 Google
// 的预算由 resolveNativeInputPolicy 按协议设置，预检和发送共用。
const (
	maxNativeInputCount      = 5
	maxNativeInputTotalBytes = 14 * 1024 * 1024
	maxNativePDFTotalBytes   = 8 * 1024 * 1024
	maxNativePDFTotalPages   = 100
	nativeDocumentMIMEPDF    = "application/pdf"
	officialOpenRouterHost   = "openrouter.ai"
)

// nativeInputKind 是可以原生发送的文件类型。
type nativeInputKind string

const (
	nativeInputPDF   nativeInputKind = "pdf"
	nativeInputAudio nativeInputKind = "audio"
	nativeInputVideo nativeInputKind = "video"
)

// 各协议能编码的音视频格式（MIME 已归一化，见 nativeInputMIME）。
var (
	// Gemini inlineData：https://ai.google.dev/gemini-api/docs/audio 与 /video-understanding。
	geminiNativeAudioMIMEs = mimeSet("audio/wav", "audio/mp3", "audio/aiff", "audio/aac", "audio/ogg", "audio/flac")
	geminiNativeVideoMIMEs = mimeSet("video/mp4", "video/mpeg", "video/mov", "video/avi", "video/x-flv", "video/mpg", "video/webm", "video/wmv", "video/3gpp")
	// OpenAI Chat Completions input_audio 只接受 wav 与 mp3。
	openAINativeAudioMIMEs = mimeSet("audio/wav", "audio/mp3")
	// OpenRouter Chat Completions：input_audio 与 video_url（data URL）。
	openRouterNativeAudioMIMEs = mimeSet("audio/wav", "audio/mp3", "audio/aiff", "audio/aac", "audio/ogg", "audio/flac", "audio/m4a")
	openRouterNativeVideoMIMEs = mimeSet("video/mp4", "video/mpeg", "video/mov", "video/webm")
)

func mimeSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// nativeInputMIME 把附件 MIME 归一化为协议文档使用的写法（如 audio/mpeg → audio/mp3、video/quicktime → video/mov）。
func nativeInputMIME(att AttachmentInput) string {
	mime := strings.ToLower(strings.TrimSpace(textutil.FirstNonEmpty(att.DetectedMIME, att.MimeType)))
	if index := strings.Index(mime, ";"); index > 0 {
		mime = strings.TrimSpace(mime[:index])
	}
	switch mime {
	case "audio/mpeg", "audio/mpeg3", "audio/x-mpeg-3":
		return "audio/mp3"
	case "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "audio/wav"
	case "audio/x-aiff":
		return "audio/aiff"
	case "audio/x-flac":
		return "audio/flac"
	case "audio/mp4", "audio/x-m4a":
		return "audio/m4a"
	case "video/quicktime":
		return "video/mov"
	case "video/x-msvideo":
		return "video/avi"
	case "video/x-ms-wmv":
		return "video/wmv"
	case "video/3gpp2":
		return "video/3gpp"
	}
	return mime
}

// nativeInputPolicy 是某条路由上可以原生发送的输入类型。
type nativeInputPolicy struct {
	Budget nativeInputBudget
	// ImageUnsupported 为 true 时模型声明不支持图片，图片不以图片内容块发送；能力未知时保持既有行为（发送）。
	// 用否定形式让零值表示「照常发送」。
	ImageUnsupported bool
	// PDF 为 true 时满足条件的 PDF 以原生文档内容块发送。
	PDF bool
	// AudioMIMEs / VideoMIMEs 为该路由可原生发送的音视频格式；为空表示不支持。
	AudioMIMEs map[string]struct{}
	VideoMIMEs map[string]struct{}
}

type nativeInputBudget struct {
	TotalBytes     int64
	PDFBytes       int64
	PDFSingleBytes int64
	NonPDFBytes    int64
	PDFPages       int
	Count          int
}

func (p nativeInputPolicy) budget() nativeInputBudget {
	if p.Budget.TotalBytes > 0 {
		return p.Budget
	}
	return nativeInputBudget{TotalBytes: maxNativeInputTotalBytes, PDFBytes: maxNativePDFTotalBytes, PDFSingleBytes: maxNativePDFTotalBytes, NonPDFBytes: maxNativeInputTotalBytes, PDFPages: maxNativePDFTotalPages, Count: maxNativeInputCount}
}

// accepts 判断附件能否在该路由上原生发送（不含预算）。
func (p nativeInputPolicy) accepts(att AttachmentInput) bool {
	switch nativeInputKindOf(att) {
	case nativeInputPDF:
		return p.PDF
	case nativeInputAudio:
		_, ok := p.AudioMIMEs[nativeInputMIME(att)]
		return ok
	case nativeInputVideo:
		_, ok := p.VideoMIMEs[nativeInputMIME(att)]
		return ok
	}
	return false
}

// nativeProtocolSupport 是路由能原生发送的内容块，已按上游可信度收敛（见文件开头第 3 条）。
type nativeProtocolSupport struct {
	pdf        bool
	audioMIMEs map[string]struct{}
	videoMIMEs map[string]struct{}
}

// resolveNativeInputPolicy 计算路由的原生输入策略。
func resolveNativeInputPolicy(route *channel.ResolvedRoute) nativeInputPolicy {
	if route == nil {
		return nativeInputPolicy{}
	}
	modalities := domainchannel.ResolveInputModalities(route.ModelCapabilitiesJSON, route.CatalogInputModalities)
	support := nativeProtocolSupportFor(route, modalities.Source == domainchannel.InputModalitiesSourceExplicit)
	policy := nativeInputPolicy{
		ImageUnsupported: modalities.Known() && !modalities.Supports(domainchannel.InputModalityImage),
		PDF:              support.pdf && modalities.Supports(domainchannel.InputModalityPDF),
	}
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterGoogleGenerateContent:
		// Inline data is base64 encoded: leave 1 MiB for JSON framing and text.
		inlineBytes := int64((100_000_000 - 1024*1024) / 4 * 3)
		policy.Budget = nativeInputBudget{TotalBytes: inlineBytes, PDFBytes: inlineBytes, PDFSingleBytes: 50_000_000, NonPDFBytes: inlineBytes, PDFPages: 200}
	case llm.AdapterOpenAIResponses, llm.AdapterOpenAIChatCompletions:
		if isOfficialOpenAIBaseURL(route.BaseURL) {
			policy.Budget = nativeInputBudget{TotalBytes: 50_000_000, PDFBytes: 50_000_000, PDFSingleBytes: 50_000_000 - 1, NonPDFBytes: maxNativeInputTotalBytes}
		}
	}
	if modalities.Supports(domainchannel.InputModalityAudio) {
		policy.AudioMIMEs = support.audioMIMEs
	}
	if modalities.Supports(domainchannel.InputModalityVideo) {
		policy.VideoMIMEs = support.videoMIMEs
	}
	return policy
}

// nativeProtocolSupportFor 返回路由协议能以原生内容块发送的类型，并按上游可信度收敛。
func nativeProtocolSupportFor(route *channel.ResolvedRoute, explicit bool) nativeProtocolSupport {
	switch llm.NormalizeAdapter(route.Protocol) {
	case llm.AdapterAnthropicMessages:
		return nativeProtocolSupport{pdf: true}
	case llm.AdapterGoogleGenerateContent:
		return nativeProtocolSupport{pdf: true, audioMIMEs: geminiNativeAudioMIMEs, videoMIMEs: geminiNativeVideoMIMEs}
	case llm.AdapterOpenAIResponses:
		return nativeProtocolSupport{pdf: explicit || isOfficialOpenAIBaseURL(route.BaseURL)}
	case llm.AdapterOpenAIChatCompletions:
		if !explicit && !isOfficialOpenAIBaseURL(route.BaseURL) {
			return nativeProtocolSupport{}
		}
		return nativeProtocolSupport{pdf: true, audioMIMEs: openAINativeAudioMIMEs}
	case llm.AdapterOpenRouterChat:
		if !explicit && !isOfficialOpenRouterBaseURL(route.BaseURL) {
			return nativeProtocolSupport{}
		}
		return nativeProtocolSupport{pdf: true, audioMIMEs: openRouterNativeAudioMIMEs, videoMIMEs: openRouterNativeVideoMIMEs}
	case llm.AdapterOpenRouterResponses:
		return nativeProtocolSupport{pdf: explicit || isOfficialOpenRouterBaseURL(route.BaseURL)}
	default:
		return nativeProtocolSupport{}
	}
}

func isOfficialOpenRouterBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.ToLower(parsed.Hostname()) == officialOpenRouterHost
}

// nativeInputKindOf 返回附件的原生输入类型；不是 PDF、音频或视频时返回空。
func nativeInputKindOf(att AttachmentInput) nativeInputKind {
	category := strings.ToLower(strings.TrimSpace(att.FileCategory))
	mime := nativeInputMIME(att)
	switch {
	case category == fileCategoryPDF || mime == nativeDocumentMIMEPDF:
		return nativeInputPDF
	case category == fileCategoryAudio || strings.HasPrefix(mime, "audio/"):
		return nativeInputAudio
	case category == fileCategoryVideo || strings.HasPrefix(mime, "video/"):
		return nativeInputVideo
	}
	return ""
}

// isUserMediaAttachment 判断附件是用户提交的音频或视频（助手生成的视频不回传给模型）。
func isUserMediaAttachment(att AttachmentInput) bool {
	kind := nativeInputKindOf(att)
	return (kind == nativeInputAudio || kind == nativeInputVideo) && isUserSubmittedAttachment(att)
}

func isUserSubmittedAttachment(att AttachmentInput) bool {
	return att.Current || strings.EqualFold(strings.TrimSpace(att.MessageRole), "user")
}

// isNativeInputCandidate 判断附件是否可以原生发送（不含路由策略与整体预算）。
//
// 资格与全文/RAG规划解耦：原生可读的文件优先使用原文件，并从解析上下文排除。
func isNativeInputCandidate(att AttachmentInput) bool {
	if !isUserSubmittedAttachment(att) || strings.TrimSpace(att.StoragePath) == "" {
		return false
	}
	if att.FileSize <= 0 {
		return false
	}
	// 提取失败的 PDF（扫描件、图片型 PDF 等）没有文本可用，但模型可以直接阅读原文件。
	if strings.EqualFold(strings.TrimSpace(att.ProcessingStatus), "failed") && nativeInputKindOf(att) != nativeInputPDF {
		return false
	}
	switch nativeInputKindOf(att) {
	case nativeInputPDF:
		return true
	case nativeInputAudio, nativeInputVideo:
		return true
	}
	return false
}

// selectNativeInputs 在预算内挑出原生发送的文件，越新的文件越优先。
// attachments 按会话中出现的先后排列，本轮文件在最后。
func selectNativeInputs(attachments []AttachmentInput, policy nativeInputPolicy) []AttachmentInput {
	budget := policy.budget()
	selected := make([]AttachmentInput, 0, maxNativeInputCount)
	seen := make(map[string]struct{})
	totalBytes, pdfBytes, pdfPages := int64(0), int64(0), 0
	for index := len(attachments) - 1; index >= 0; index-- {
		att := attachments[index]
		fileID := strings.TrimSpace(att.FileID)
		if _, duplicated := seen[fileID]; duplicated || fileID == "" || !policy.accepts(att) || !isNativeInputCandidate(att) {
			continue
		}
		if (budget.Count > 0 && len(selected) >= budget.Count) || totalBytes+att.FileSize > budget.TotalBytes {
			continue
		}
		isPDF := nativeInputKindOf(att) == nativeInputPDF
		if !isPDF && totalBytes-pdfBytes+att.FileSize > budget.NonPDFBytes {
			continue
		}
		if isPDF && (att.FileSize > budget.PDFSingleBytes || pdfBytes+att.FileSize > budget.PDFBytes || (budget.PDFPages > 0 && (att.PageCount <= 0 || pdfPages+att.PageCount > budget.PDFPages))) {
			continue
		}
		seen[fileID] = struct{}{}
		totalBytes += att.FileSize
		if isPDF {
			pdfBytes += att.FileSize
			pdfPages += att.PageCount
		}
		selected = append(selected, att)
	}
	return selected
}

func markNativeAttachments(attachments []AttachmentInput, native nativeInputs) []AttachmentInput {
	result := append([]AttachmentInput(nil), attachments...)
	for i := range result {
		if part, ok := native.Parts[strings.TrimSpace(result[i].FileID)]; ok {
			result[i].ContextMode = fileContextModeNativeDocument
			if nativeInputKindOf(result[i]) == nativeInputPDF {
				result[i].PageCount = part.PageCount
			}
		}
	}
	return result
}

// Read failures are fatal for this turn. In particular, a green preflight must
// never turn into a successful generation that omitted an unreadable attachment.
func validateCurrentNativeReads(attachments []AttachmentInput, native nativeInputs) error {
	for _, att := range attachments {
		if att.Current && native.Selected[strings.TrimSpace(att.FileID)] {
			if _, ok := native.Parts[strings.TrimSpace(att.FileID)]; !ok {
				return ErrFileProcessingNotReady
			}
		}
	}
	for _, att := range attachments {
		if !att.Current {
			continue
		}
		if _, ok := native.Parts[strings.TrimSpace(att.FileID)]; ok {
			continue
		}
		// Audio/video have no parsed-context path in this version (see
		// canUseAttachmentFullContext), so metadata or stray text is not a fallback.
		if isUserMediaAttachment(att) && !native.Policy.accepts(att) {
			return ErrFileUnsupportedByModel
		}
		if isUserMediaAttachment(att) && isNativeInputCandidate(att) {
			// Supported media omitted by the selector exceeded the request budget;
			// waiting for extraction cannot make an audio/video file smaller.
			return ErrFileTooLarge
		}
		if isUserMediaAttachment(att) && att.ContextMode != "" {
			return ErrFileProcessingNotReady
		}
		if native.Policy.accepts(att) && strings.TrimSpace(att.ExtractedText) == "" && att.ContextMode != "" && att.ContextMode != fileContextModeRAG {
			return ErrFileProcessingNotReady
		}
	}
	return nil
}

// nativeInputCache 缓存一次发送内已读取的原生文件（按 fileID），读取失败也会记录。
// 同一次发送中，处理过程展示、首条路由与故障转移路由共用它，三者看到的原生决定一致，也不会重复读取存储。
type nativeInputCache struct {
	parts map[string]*llm.ContentPart
}

func newNativeInputCache() *nativeInputCache {
	return &nativeInputCache{parts: make(map[string]*llm.ContentPart)}
}

// nativeInputs 是某条路由上的原生输入结果。
type nativeInputs struct {
	Policy   nativeInputPolicy
	Selected map[string]bool
	// Parts 为实际原生发送的内容块（按 fileID）；读取失败的文件不在其中。
	Parts map[string]llm.ContentPart
}

// unsupported 判断附件在该路由上无法提供：模型不支持的图片，以及无法原生发送的用户音视频
// （没有文本可回退）。超出预算或读取失败的音视频不算，按「无法读取」处理。
func (n nativeInputs) unsupported(att AttachmentInput) bool {
	if _, native := n.Parts[strings.TrimSpace(att.FileID)]; native {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(att.ContextMode), fileContextModeDirectImage) {
		return n.Policy.ImageUnsupported
	}
	return isUserMediaAttachment(att) && !n.Policy.accepts(att)
}

// resolveNativeInputs 计算路由策略并读取预算内的原始内容。读取失败的文件不在
// 结果中；发送入口和每次路由规划必须校验本轮文件，确保调用上游前发现失败。
func (s *Service) resolveNativeInputs(ctx context.Context, route *channel.ResolvedRoute, attachments []AttachmentInput, cache *nativeInputCache) nativeInputs {
	result := nativeInputs{Policy: resolveNativeInputPolicy(route)}
	attachments = s.resolveNativePDFPageCounts(ctx, attachments, result.Policy, cache)
	selected := selectNativeInputs(attachments, result.Policy)
	if len(selected) == 0 {
		return result
	}
	if cache == nil {
		cache = newNativeInputCache()
	}
	result.Selected = make(map[string]bool, len(selected))
	for _, att := range selected {
		result.Selected[strings.TrimSpace(att.FileID)] = true
	}
	missing := make([]AttachmentInput, 0, len(selected))
	for _, att := range selected {
		if _, loaded := cache.parts[strings.TrimSpace(att.FileID)]; !loaded {
			missing = append(missing, att)
		}
	}
	if len(missing) > 0 {
		loaded := s.loadNativeInputParts(ctx, missing)
		for _, att := range missing {
			fileID := strings.TrimSpace(att.FileID)
			if part, ok := loaded[fileID]; ok {
				cache.parts[fileID] = &part
			} else {
				cache.parts[fileID] = nil
			}
		}
	}
	result.Parts = make(map[string]llm.ContentPart, len(selected))
	for _, att := range selected {
		if part := cache.parts[strings.TrimSpace(att.FileID)]; part != nil {
			result.Parts[strings.TrimSpace(att.FileID)] = *part
		}
	}
	return result
}

func (s *Service) resolveNativePDFPageCounts(ctx context.Context, attachments []AttachmentInput, policy nativeInputPolicy, cache *nativeInputCache) []AttachmentInput {
	if !policy.PDF || policy.budget().PDFPages <= 0 {
		return attachments
	}
	result := append([]AttachmentInput(nil), attachments...)
	for i, att := range result {
		if att.PageCount > 0 || nativeInputKindOf(att) != nativeInputPDF || !isNativeInputCandidate(att) || att.FileSize > policy.budget().PDFSingleBytes {
			continue
		}
		if cache != nil {
			if part := cache.parts[strings.TrimSpace(att.FileID)]; part != nil && part.PageCount > 0 {
				result[i].PageCount = part.PageCount
				continue
			}
		}
		if s.extractSvc == nil {
			continue
		}
		pages, err := s.extractSvc.ProbeStoredPDFPageCount(ctx, att.StoragePath)
		if err == nil && pages > 0 {
			result[i].PageCount = pages
			if cache != nil && cache.parts[strings.TrimSpace(att.FileID)] != nil {
				cache.parts[strings.TrimSpace(att.FileID)].PageCount = pages
			}
		}
	}
	return result
}

// routeAttachmentTraceItems 把与路由相关的决定写入处理过程展示用的附件副本：
// 原生发送的文件标记为 native_document，无法提供的图片与音视频标记为 unsupported。
func routeAttachmentTraceItems(items []AttachmentInput, native nativeInputs, skipImages bool) []AttachmentInput {
	result := make([]AttachmentInput, len(items))
	copy(result, items)
	for index := range result {
		fileID := strings.TrimSpace(result[index].FileID)
		if _, ok := native.Parts[fileID]; ok {
			result[index].ContextMode = fileContextModeNativeDocument
			continue
		}
		isImage := result[index].ContextMode == fileContextModeDirectImage
		if (isImage && skipImages) || !native.unsupported(result[index]) {
			continue
		}
		result[index].ContextMode = fileContextModeUnsupported
	}
	return result
}

// loadNativeInputParts 读取选中文件的原始字节。单个文件读取失败时跳过它。
func (s *Service) loadNativeInputParts(ctx context.Context, attachments []AttachmentInput) map[string]llm.ContentPart {
	if len(attachments) == 0 || s == nil || s.storeProvider == nil {
		return nil
	}
	store, err := s.storeProvider.Open(ctx)
	if err != nil {
		s.warnNativeInput("native_input_store_open_failed", "", err)
		return nil
	}
	parts := make(map[string]llm.ContentPart, len(attachments))
	for _, att := range attachments {
		data, err := readNativeInput(ctx, store, att)
		if err != nil {
			s.warnNativeInput("native_input_read_failed", att.FileID, err)
			continue
		}
		parts[strings.TrimSpace(att.FileID)] = newNativeInputPart(att, data)
	}
	return parts
}

func newNativeInputPart(att AttachmentInput, data []byte) llm.ContentPart {
	part := llm.ContentPart{Data: data, FileName: strings.TrimSpace(att.FileName), MimeType: nativeInputMIME(att)}
	switch nativeInputKindOf(att) {
	case nativeInputAudio:
		part.Kind = llm.ContentPartAudio
		part.DurationSeconds = att.DurationSeconds
	case nativeInputVideo:
		part.Kind = llm.ContentPartVideo
		part.DurationSeconds = att.DurationSeconds
	default:
		part.Kind = llm.ContentPartDocument
		part.MimeType = nativeDocumentMIMEPDF
		part.PageCount = att.PageCount
	}
	return part
}

func readNativeInput(ctx context.Context, store objectstorage.Store, att AttachmentInput) ([]byte, error) {
	reader, _, err := store.Open(ctx, strings.TrimSpace(att.StoragePath))
	if err != nil {
		return nil, err
	}
	defer reader.Close() //nolint:errcheck
	// Route selection has already validated the declared size. The read itself
	// must stay bounded as object storage metadata may be stale.
	limit := att.FileSize
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) != limit {
		return nil, fmt.Errorf("native input size %d outside limit", len(data))
	}
	return data, nil
}

func (s *Service) warnNativeInput(message string, fileID string, err error) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Warn(message, zap.String("file_id", strings.TrimSpace(fileID)), zap.Error(err))
}

// attachmentRouteTrace 是「文件上下文」处理阶段的展示内容，随路由重新计算。
type attachmentRouteTrace struct {
	fileMode   string
	items      []AttachmentInput
	skipImages bool

	summary  string
	markdown string
	payload  *tracePayload
}

func (t *attachmentRouteTrace) resolve(ctx context.Context, s *Service, route *channel.ResolvedRoute, attachments []AttachmentInput, cache *nativeInputCache) {
	native := s.resolveNativeInputs(ctx, route, attachments, cache)
	items := routeAttachmentTraceItems(t.items, native, t.skipImages)
	t.summary, t.markdown, t.payload = buildAttachmentProcessTrace(t.fileMode, items)
}

func (t *attachmentRouteTrace) visible() bool {
	return shouldShowAttachmentProcessTrace(t.items)
}
