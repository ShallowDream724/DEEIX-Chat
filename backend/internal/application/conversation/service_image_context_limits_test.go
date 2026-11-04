package conversation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	storageport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestConversationImageContextSendsAllTwelveImagesAndTraceIdentities(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	messages := make([]model.Message, 0, 12)
	attachments := make([]AttachmentInput, 0, 12)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("image-%d", i)
		data := []byte(id)
		if _, err := store.Put(t.Context(), id, bytes.NewReader(data), storageport.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, model.Message{Role: "user", Attachments: fmt.Sprintf(`[{"file_id":%q,"kind":"image"}]`, id)})
		attachments = append(attachments, AttachmentInput{FileID: id, FileName: "image.png", Kind: "image", StoragePath: id, FileSize: int64(len(data)), ContextMode: fileContextModeDirectImage})
	}
	service := &Service{storeProvider: &conversationTestStoreProvider{store: store}}
	got, err := service.injectConversationImageContext(t.Context(), historyMessagesFromDomain(messages, historyMessageOptions{}), messages, attachments, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i, message := range got {
		if len(message.Parts) != 1 || string(message.Parts[0].Data) != fmt.Sprintf("image-%d", i) {
			t.Fatalf("image %d missing or moved: %#v", i, message)
		}
	}
	_, _, trace := buildAttachmentProcessTrace("auto", attachmentProcessTraceItems(attachments))
	if len(trace.FileRefs) != 12 || trace.FileRefs[0].FileID != "image-0" || trace.FileRefs[11].FileID != "image-11" {
		t.Fatalf("same-name files must retain distinct identities: %#v", trace.FileRefs)
	}
}

func TestFinalAttachmentTraceUsesSurvivingFileIdentity(t *testing.T) {
	items := []AttachmentInput{
		{FileID: "removed", FileName: "same.png", ContextMode: fileContextModeDirectImage},
		{FileID: "retained", FileName: "same.png", ContextMode: fileContextModeDirectImage},
		{FileID: "unsupported", ContextMode: fileContextModeUnsupported},
	}
	messages := []llm.Message{{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartImage, FileName: "same.png", SourceFileIDs: []string{"retained"}, Data: []byte("image")}}}}
	got := attachmentTraceItemsForMessages(items, messages)
	if got[0].ContextMode != fileContextModeSkipped || got[1].ContextMode != fileContextModeDirectImage || got[2].ContextMode != fileContextModeUnsupported {
		t.Fatalf("final trace must distinguish same-name files by retained IDs: %#v", got)
	}
	if items[0].ContextMode != fileContextModeDirectImage {
		t.Fatal("final trace reconciliation must preserve the plan for the next route")
	}
	got = attachmentTraceItemsForMessages(items, nil)
	if got[1].ContextMode != fileContextModeSkipped {
		t.Fatalf("a trimmed final prompt must not claim the file was sent: %#v", got)
	}
}

func TestInputLimitWrappingPreservesPublicIdentityAndTypedReason(t *testing.T) {
	err := wrapUpstreamRequestError(llm.NewInputLimitError("imageCount", 3, 2))
	if !errors.Is(err, ErrFileTooLarge) || !errors.Is(err, llm.ErrInputTooLarge) || errors.Is(err, ErrUpstreamRequestFailed) {
		t.Fatalf("input budget error identity changed: %v", err)
	}
	if detail := llm.InputLimitDetails(err); detail == nil || detail.Actual != 3 || detail.Limit != 2 {
		t.Fatalf("typed reason was lost: %#v", detail)
	}
}

func TestRouteImageBudgetSharesHistoryAndCurrentImages(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	for _, id := range []string{"old", "current"} {
		if _, err := store.Put(t.Context(), id, bytes.NewReader([]byte(id)), storageport.PutOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{InputLimitsJSON: `{"defaults":{"imageCount":1}}`}
	service := &Service{cfg: config.NewRuntime(cfg), storeProvider: &conversationTestStoreProvider{store: store}}
	current := AttachmentInput{FileID: "current", FileName: "image.png", Kind: "image", MimeType: "image/png", StoragePath: "current", Current: true, ContextMode: fileContextModeDirectImage}
	input := messageRoutePromptInput{
		DomainMessages:    []model.Message{{Role: "user", Content: "old question", Attachments: `[{"file_id":"old","kind":"image"}]`}, {Role: "assistant", Content: "old answer"}, {Role: "user", Content: "compare"}},
		ConversationFiles: []AttachmentInput{{FileID: "old", FileName: "image.png", Kind: "image", MimeType: "image/png", StoragePath: "old", ContextMode: fileContextModeDirectImage}, current},
		DynamicContext:    userContextInput{Attachments: []AttachmentInput{current}}, Config: cfg,
	}
	route := &channel.ResolvedRoute{Protocol: llm.AdapterOpenAIChatCompletions, BaseURL: "https://relay.example/v1", CatalogInputModalities: []string{"text", "image"}}
	if _, err := service.buildMessageRoutePrompt(t.Context(), route, input); !errors.Is(err, llm.ErrInputTooLarge) {
		t.Fatalf("expected combined request image budget failure, got %v", err)
	}
	cfg.InputLimitsJSON = `{"defaults":{"imageCount":2,"imageBytes":10}}`
	service.cfg = config.NewRuntime(cfg)
	input.Config = cfg
	plan, err := service.buildMessageRoutePrompt(t.Context(), route, input)
	if err != nil {
		t.Fatal(err)
	}
	var images []llm.ContentPart
	for _, message := range plan.Messages {
		for _, part := range message.Parts {
			if part.Kind == llm.ContentPartImage {
				images = append(images, part)
			}
		}
	}
	if len(images) != 2 || string(images[0].Data) != "old" || string(images[1].Data) != "current" {
		t.Fatalf("expected both images at exact count/byte boundary, got %#v", images)
	}
	if err := store.Delete(t.Context(), "current"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.buildMessageRoutePrompt(t.Context(), route, input); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("current image read errors must stop prompt planning: %v", err)
	}
}

func TestConversationImageContextRetainsSummaryCoveredBranchImages(t *testing.T) {
	store := objectstorage.NewLocal(t.TempDir())
	if _, err := store.Put(t.Context(), "covered", bytes.NewReader([]byte("original-image")), storageport.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	service := &Service{storeProvider: &conversationTestStoreProvider{store: store}}
	messages := []model.Message{{Role: "system", Content: "Summary of earlier turns"}, {Role: "user", Content: "Read the earlier image again"}}
	attachments := []AttachmentInput{{FileID: "covered", FileName: "earlier.png", Kind: "image", StoragePath: "covered", MessageRole: "user", ContextMode: fileContextModeDirectImage}}
	got, err := service.injectConversationImageContext(t.Context(), historyMessagesFromDomain(messages, historyMessageOptions{}), messages, attachments, config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[1].Parts) != 2 || got[1].Parts[0].Kind != llm.ContentPartImage || string(got[1].Parts[0].Data) != "original-image" || got[1].Parts[0].FileName != "earlier.png" {
		t.Fatalf("summary must retain the original branch image: %#v", got)
	}
}

func TestConversationImageBudgetCountsRepeatedFileReferences(t *testing.T) {
	messages := []model.Message{{Role: "user", Attachments: `[{"file_id":"same","kind":"image"}]`}, {Role: "assistant", Content: "answer"}, {Role: "user", Attachments: `[{"file_id":"same","kind":"image"}]`}}
	attachment := AttachmentInput{FileID: "same", Kind: "image", Current: true, ContextMode: fileContextModeDirectImage}
	refs := conversationImageRefs(messages, []AttachmentInput{attachment})
	if len(refs) != 1 || refs[0].messageIndex != 0 {
		t.Fatalf("reattaching this turn must retain the earlier occurrence: %#v", refs)
	}
	part := llm.ContentPart{Kind: llm.ContentPartImage, Data: []byte("same")}
	if err := validateConversationImageBudget([]llm.Message{{Parts: []llm.ContentPart{part, part}}}, inputlimits.Limits{ImageCount: 1}); !errors.Is(err, llm.ErrInputTooLarge) {
		t.Fatalf("repeated references consume separate request slots: %v", err)
	}
}

func TestConversationImageDimensionZeroAndCacheParameterIsolation(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 3200, 2))); err != nil {
		t.Fatal(err)
	}
	store := objectstorage.NewLocal(t.TempDir())
	if _, err := store.Put(t.Context(), "wide", bytes.NewReader(encoded.Bytes()), storageport.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	service := &Service{storeProvider: &conversationTestStoreProvider{store: store}, imageContextCache: defaultPreparedConversationImageCache()}
	attachment := AttachmentInput{FileID: "wide", Kind: "image", MimeType: "image/png", StoragePath: "wide", Current: true, FileSize: int64(encoded.Len())}
	cfg := config.Config{PreparedImageCacheMaxEntries: 64, PreparedImageCacheMaxBytes: 64 * 1024 * 1024}
	preparedBytes := 0
	for _, dimension := range []int{1024, 3072, 0, 1024} {
		cfg.ImageMaxDimension = dimension
		parts, err := service.prepareUserContextImages(t.Context(), []AttachmentInput{attachment}, cfg, inputlimits.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(parts[0].Data))
		want := dimension
		if want == 0 {
			want = 3200
			if !bytes.Equal(parts[0].Data, encoded.Bytes()) {
				t.Fatal("dimension zero must preserve original image bytes")
			}
		}
		if err != nil || decoded.Bounds().Dx() != want {
			t.Fatalf("dimension %d: expected width %d, decode error %v", dimension, want, err)
		}
		preparedBytes = len(parts[0].Data)
	}
	if preparedBytes >= encoded.Len() {
		t.Fatal("test image must shrink before testing the prepared image budget")
	}
	if _, err := service.prepareUserContextImages(t.Context(), []AttachmentInput{attachment}, cfg, inputlimits.Limits{ImageSingleBytes: int64(preparedBytes)}); err != nil {
		t.Fatalf("source above the send limit must pass when the prepared image fits: %v", err)
	}
	if _, err := service.prepareUserContextImages(t.Context(), []AttachmentInput{attachment}, cfg, inputlimits.Limits{ImageSingleBytes: int64(preparedBytes - 1)}); !errors.Is(err, llm.ErrInputTooLarge) {
		t.Fatalf("new prepared image budget must apply even after cache hit: %v", err)
	}
	attachment.Current = false
	parts, err := service.prepareUserContextImages(t.Context(), []AttachmentInput{attachment}, cfg, inputlimits.Limits{})
	if err != nil || !parts[0].Dynamic {
		t.Fatalf("retrieved images must remain dynamic: %v, %#v", err, parts)
	}
	processor, err := prepareImageAttachmentForProcessor(t.Context(), store, attachment, 0)
	if err != nil || !bytes.Equal(processor.data, encoded.Bytes()) {
		t.Fatalf("MCP dimension zero must preserve original bytes: %v", err)
	}
}

type imageSourceMetadataStore struct {
	storageport.Store
	data []byte
	size int64
}

func (s imageSourceMetadataStore) Open(context.Context, string) (io.ReadCloser, storageport.ObjectInfo, error) {
	return io.NopCloser(bytes.NewReader(s.data)), storageport.ObjectInfo{SizeBytes: s.size}, nil
}

func TestImageSourceReadsCompleteBeyondOldFiftyMiBLimit(t *testing.T) {
	data := bytes.Repeat([]byte{0xab}, 50*1024*1024+17)
	store := imageSourceMetadataStore{data: data, size: int64(len(data))}
	got, err := readConversationImageSource(t.Context(), store, AttachmentInput{FileID: "large", StoragePath: "large"})
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("source must be complete beyond former fixed read limit: length %d, error %v", len(got), err)
	}
	for _, size := range []int64{int64(len(data) - 1), int64(len(data) + 1)} {
		store.size = size
		if _, err := readConversationImageSource(t.Context(), store, AttachmentInput{FileID: "large", StoragePath: "large"}); !errors.Is(err, ErrInvalidFileReference) {
			t.Fatalf("inconsistent source metadata must fail explicitly, size %d: %v", size, err)
		}
	}
}
