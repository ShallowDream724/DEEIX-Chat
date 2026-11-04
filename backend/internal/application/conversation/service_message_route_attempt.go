package conversation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	domainuicomponent "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/uicomponent"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type messageRoutePromptInput struct {
	UserID                   uint
	UserContent              string
	ProjectSystemPrompt      string
	HTMLVisualPromptEnabled  bool
	UIComponents             []domainuicomponent.Component
	UIBlockVersion           int
	ReasoningContentPassback bool
	DomainMessages           []model.Message
	// ConversationFiles 是本轮文件规划结果（含历史轮次与本轮，已带 ContextMode）。
	// 文本文件随所属用户轮次渲染，图片作为图片内容块随所属轮次发送。
	ConversationFiles []AttachmentInput
	// NativeInputs 缓存本次发送已读取的原生文件，路由之间共用；为空时按需读取。
	NativeInputs         *nativeInputCache
	DynamicContext       userContextInput
	PreferencePrompt     string
	SkillPrompts         *skillPrompts
	ToolRuntime          selectedToolRuntime
	SkipImageAttachments bool
	Config               config.Config
}

func withMessageRouteReasoningPassbackOptions(
	options map[string]any,
	inputOptions map[string]any,
	route *channel.ResolvedRoute,
	reasoningContentPassback bool,
	messages []llm.Message,
) map[string]any {
	if route == nil || !shouldApplyReasoningPassbackRequestOptions(
		reasoningContentPassback,
		route.ReasoningPassbackRequestOptions,
		messages,
	) {
		return options
	}
	return withReasoningPassbackRequestOptions(
		options,
		route.ReasoningPassbackRequestOptions,
		inputOptions,
		route.ModelCapabilitiesJSON,
	)
}

// planRoutePrompt 按路由决定推理内容是否回传后构建提示词；路由故障转移时对新路由重新规划。
// 返回值中的 bool 是该路由生效的推理回传开关。
func (s *Service) planRoutePrompt(
	ctx context.Context,
	userID uint,
	base messageRoutePromptInput,
	route *channel.ResolvedRoute,
) (PromptPlan, bool, error) {
	passbackEnabled := s.reasoningContentPassbackEnabled(ctx, userID, route)
	base.UserID = userID
	base.ReasoningContentPassback = passbackEnabled
	plan, err := s.buildMessageRoutePrompt(ctx, route, base)
	return plan, passbackEnabled, err
}

func (s *Service) buildMessageRoutePrompt(ctx context.Context, route *channel.ResolvedRoute, input messageRoutePromptInput) (PromptPlan, error) {
	// 模型上下文预算在最终 GenerateInput 完整组装后统一执行。这里保留完整活跃
	// 分支，避免先按历史消息耗尽预算，再遗漏文件、RAG、Skill 与工具定义开销。
	routeMessages := input.DomainMessages
	if len(routeMessages) == 0 && len(input.ConversationFiles) > 0 {
		routeMessages = []model.Message{{Role: "user", Content: input.UserContent}}
	}
	historyMessages := historyMessagesFromDomain(routeMessages, historyMessageOptions{
		ReasoningContentPassback: input.ReasoningContentPassback,
	})
	// 原生输入按路由判断：故障转移到另一条路由时重新计算，不把原生内容块发给不支持它的协议。
	native := s.resolveNativeInputs(ctx, route, input.ConversationFiles, input.NativeInputs, input.Config)
	if err := validateRequiredNativeInputs(input.ConversationFiles, native); err != nil {
		return PromptPlan{}, err
	}
	files, err := s.resolveRouteParsedAttachments(ctx, input, native)
	if err != nil {
		return PromptPlan{}, err
	}
	if err := validatePlannedFileContext(files, input.Config); err != nil {
		return PromptPlan{}, err
	}
	dynamicContext := input.DynamicContext
	imageMessages := routeMessages
	if input.SkipImageAttachments {
		imageMessages, files, dynamicContext.Attachments = withoutProcessedRouteImages(imageMessages, files, dynamicContext)
	}
	for _, att := range files {
		if att.ContextMode == fileContextModeDirectImage && native.Policy.ImageUnsupported {
			return PromptPlan{}, attachmentInputError(att, ErrFileUnsupportedByModel)
		}
	}
	// 文件按所属轮次就位必须早于图片注入与同角色合并：二者都依赖未合并的历史下标。
	documents := placeTurnDocuments(historyMessages, routeMessages, files, turnDocumentOptions{
		Native: native,
	})
	historyMessages = documents.Messages
	if native.Policy.ImageUnsupported {
		// 检索出的图片证据可以省略；用户附件已在上面检查。
		dynamicContext.Attachments = nil
		dynamicContext.PreparedImages = nil
	}
	if !native.Policy.ImageUnsupported {
		dynamicContext.Attachments = mergeCurrentRouteImages(dynamicContext.Attachments, files)
		limits := native.Policy.InputLimits
		if limits.ImageCount > 0 {
			historicalCount := len(conversationImageRefs(imageMessages, files))
			count := historicalCount + len(dynamicContext.Attachments)
			if count > limits.ImageCount {
				return PromptPlan{}, attachmentInputError(AttachmentInput{Current: historicalCount <= limits.ImageCount},
					llm.NewInputLimitError("imageCount", int64(count), int64(limits.ImageCount)))
			}
		}
		// Per-file byte checks apply during reads. Aggregate bytes are measured
		// after both historical and current images have been prepared.
		readLimits := limits
		readLimits.ImageCount, readLimits.ImageBytes = 0, 0
		historyMessages, err = s.injectConversationImageContext(ctx, historyMessages, imageMessages, files, input.Config, readLimits)
		if err != nil {
			return PromptPlan{}, err
		}
		dynamicContext.PreparedImages, err = s.prepareUserContextImages(ctx, dynamicContext.Attachments, input.Config, native.Policy.InputLimits)
		if err != nil {
			return PromptPlan{}, err
		}
		// Count every occurrence in the actual request. History and this turn
		// share one budget, including dynamically retrieved images.
		imageMessages := append(cloneLLMMessages(historyMessages), llm.Message{Parts: dynamicContext.PreparedImages})
		if err := validateConversationImageBudget(imageMessages, native.Policy.InputLimits); err != nil {
			historyFits := validateConversationImageBudget(historyMessages, limits) == nil
			return PromptPlan{}, attachmentInputError(AttachmentInput{Current: historyFits}, err)
		}
	}
	// 图片注入按「过滤后的历史下标」对齐，必须在注入之后合并，避免下标错位。
	historyMessages = mergeConsecutiveSameRoleMessages(historyMessages)
	if len(historyMessages) == 0 {
		historyMessages = append(historyMessages, llm.Message{Role: "user", Content: input.UserContent})
	}

	// ContextAssembler 只负责稳定的槽位排序与去重；最终模型窗口由完整请求预算器
	// 统一约束，避免旧的固定 32K 上限提前丢弃偏好等系统上下文。
	assembler := NewContextAssembler(0)
	systemPrompt := resolveMessageSystemPromptInjection(input.Config, route, input.ProjectSystemPrompt, requestPromptOptions{HTMLVisual: input.HTMLVisualPromptEnabled, UIComponents: input.UIComponents, UIBlockVersion: input.UIBlockVersion})
	if systemPrompt.Content != "" {
		if systemPrompt.InlineToUser {
			historyMessages = inlineSystemPromptIntoLatestUserMessage(historyMessages, systemPrompt.Content)
		} else {
			assembler.Add(ContextSlot{Kind: SlotSystemPrompt, Content: systemPrompt.Content, Required: true})
		}
	}
	if input.PreferencePrompt != "" {
		assembler.Add(ContextSlot{Kind: SlotPreference, Content: input.PreferencePrompt})
	}
	baseMessages, _ := assembler.Assemble(historyMessages)
	return buildPromptPlan(ctx, promptPlanInput{
		BaseMessages:   baseMessages,
		TurnDocuments:  documents,
		DynamicContext: dynamicContext,
		SkillPrompts:   input.SkillPrompts,
		ToolRuntime:    input.ToolRuntime,
		Config:         input.Config,
		StoreProvider:  s.storeProvider,
	}), nil
}

func mergeCurrentRouteImages(dynamic, files []AttachmentInput) []AttachmentInput {
	seen := make(map[string]bool, len(dynamic))
	for _, att := range dynamic {
		seen[strings.TrimSpace(att.FileID)] = true
	}
	var missing []AttachmentInput
	for _, att := range files {
		id := strings.TrimSpace(att.FileID)
		if att.Current && att.ContextMode == fileContextModeDirectImage && !seen[id] {
			missing = append(missing, att)
			seen[id] = true
		}
	}
	if len(missing) == 0 {
		return dynamic
	}
	return append(append([]AttachmentInput(nil), dynamic...), missing...)
}

func (s *Service) resolveRouteParsedAttachments(ctx context.Context, input messageRoutePromptInput, native nativeInputs) ([]AttachmentInput, error) {
	files := markNativeAttachments(input.ConversationFiles, native)
	for i, att := range files {
		if att.ContextMode != fileContextModeNativeDocument {
			continue
		}
		if _, ok := native.Parts[strings.TrimSpace(att.FileID)]; ok {
			continue
		}
		// The initial route read the original PDF and bypassed extraction.
		// A text-only fallback can send the parsed document if it is available.
		att.ContextMode = fileContextModeFull
		if strings.TrimSpace(att.ExtractedText) == "" && s.processingSvc != nil && input.UserID != 0 {
			hydrated, err := s.hydrateAttachmentsForSend(ctx, input.UserID, []AttachmentInput{att}, nil)
			if err != nil {
				return nil, err
			}
			att = hydrated[0]
		}
		files[i] = att
	}
	return files, nil
}

func withoutProcessedRouteImages(messages []model.Message, files []AttachmentInput, dynamic userContextInput) ([]model.Message, []AttachmentInput, []AttachmentInput) {
	processed := make(map[string]bool)
	for _, analysis := range dynamic.ImageAnalyses {
		processed[strings.TrimSpace(analysis.FileID)] = true
	}
	if len(processed) == 0 {
		return messages, files, dynamic.Attachments
	}
	filter := func(items []AttachmentInput) []AttachmentInput {
		result := make([]AttachmentInput, 0, len(items))
		for _, att := range items {
			if !processed[strings.TrimSpace(att.FileID)] {
				result = append(result, att)
			}
		}
		return result
	}
	history := append([]model.Message(nil), messages...)
	for i := range history {
		refs := parseAttachmentSnapshotRefs(history[i].Attachments)
		remaining := make([]attachmentSnapshotRef, 0, len(refs))
		for _, ref := range refs {
			if !processed[strings.TrimSpace(ref.FileID)] {
				remaining = append(remaining, ref)
			}
		}
		if len(remaining) != len(refs) {
			encoded, _ := json.Marshal(remaining)
			history[i].Attachments = string(encoded)
		}
	}
	return history, filter(files), filter(dynamic.Attachments)
}
