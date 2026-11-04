package conversation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestAttachmentReadinessUsesSelectedChannelsInsteadOfLegacyGlobalCount(t *testing.T) {
	cfg := config.Config{MaxMessageFiles: 10, InputLimitsJSON: `{"upstreams":{"1":{"messageFileCount":20,"imageCount":7},"2":{"messageFileCount":0},"3":{"imageCount":4},"4":{"messageFileCount":10}}}`}
	first := &channel.ResolvedRoute{UpstreamID: 1}
	unlimited := &channel.ResolvedRoute{UpstreamID: 2}
	stricter := &channel.ResolvedRoute{UpstreamID: 3}
	sameAsStricter := &channel.ResolvedRoute{UpstreamID: 4}
	routes := &nativeReadinessRoutes{routes: []*channel.ResolvedRoute{first, stricter}}
	ids := make([]string, 21)
	files := make([]model.FileObject, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("file-%d", i)
		files[i] = model.FileObject{FileID: ids[i], FileCategory: "document", ProcessingStatus: "pending"}
	}
	s := &Service{cfg: config.NewRuntime(cfg), routeResolver: routes, repo: nativeReadinessRepo{owner: 1, files: files}}
	input := NativeReadinessInput{UserID: 1, Model: "model"}
	result, err := s.GetNativeReadiness(t.Context(), input)
	if err != nil || result.MessageFileCount != 20 || result.Limits.ImageCount != 4 || len(result.Issues) != 0 {
		t.Fatalf("empty preflight must expose the largest candidate count and shared media limits: %+v (%v)", result, err)
	}
	input.FileIDs = ids[:20]
	result, err = s.GetNativeReadiness(t.Context(), input)
	if err != nil || result.MessageFileCount != 20 || len(result.Issues) != 0 {
		t.Fatalf("20 files should not be blocked because one candidate only allows 10: %+v (%v)", result, err)
	}
	input.FileIDs = ids
	result, err = s.GetNativeReadiness(t.Context(), input)
	if err != nil || len(result.Issues) != 0 {
		t.Fatalf("different measured limits should be resolved during send: %+v (%v)", result, err)
	}
	routes.routes = append(routes.routes, unlimited)
	result, err = s.GetNativeReadiness(t.Context(), input)
	if err != nil || result.MessageFileCount != 0 || len(result.Issues) != 0 {
		t.Fatalf("an unlimited candidate must leave the selection count unbounded: %+v (%v)", result, err)
	}
	routes.routes = []*channel.ResolvedRoute{stricter, sameAsStricter}
	result, err = s.GetNativeReadiness(t.Context(), input)
	want := []AttachmentReadinessIssue{{Dimension: "messageFileCount", Actual: 21, Limit: 10, Scope: "current"}}
	if err != nil || result.MessageFileCount != 10 || !reflect.DeepEqual(result.Issues, want) {
		t.Fatalf("identical issues across all channels must still block: %+v (%v)", result, err)
	}
	if err := validateRouteMessageFileCount(cfg, first, 13); err != nil {
		t.Fatalf("actual route override: %v", err)
	}
	if err := validateRouteMessageFileCount(cfg, stricter, 13); !errors.Is(err, ErrTooManyMessageFiles) || llm.InputLimitDetails(err) == nil {
		t.Fatalf("actual route must enforce its own count and attach details: %v", err)
	}
}

type readinessBranchRepo struct {
	nativeReadinessRepo
	anchor       model.Message
	history      []model.Message
	ancestorLeaf uint
}

func (r *readinessBranchRepo) GetConversationByPublicID(_ context.Context, publicID string, userID uint) (*model.Conversation, error) {
	if publicID != "chat" || userID != r.owner {
		return nil, ErrConversationNotFound
	}
	return &model.Conversation{ID: 10, Model: "saved-model"}, nil
}

func (r *readinessBranchRepo) GetMessageByPublicID(_ context.Context, conversationID, userID uint, publicID string) (*model.Message, error) {
	if conversationID != 10 || userID != r.owner || publicID != r.anchor.PublicID {
		return nil, ErrInvalidMessageBranch
	}
	return &r.anchor, nil
}

func (r *readinessBranchRepo) ListMessageAncestors(_ context.Context, conversationID, leafID uint, _ int) ([]model.Message, error) {
	if conversationID != 10 || leafID != r.anchor.ID {
		return nil, ErrInvalidMessageBranch
	}
	r.ancestorLeaf = leafID
	return r.history, nil
}

func TestAttachmentReadinessChecksOnlyExplicitOwnedAncestorBranch(t *testing.T) {
	ancestor := model.Message{ID: 20, PublicID: "chosen-parent", Role: "user", Status: "success", Attachments: `[{"file_id":"history"},{"file_id":"foreign"}]`}
	repo := &readinessBranchRepo{
		nativeReadinessRepo: nativeReadinessRepo{owner: 1, files: []model.FileObject{
			{FileID: "current", FileName: "current.png", FileCategory: "image", DetectedMIME: "image/png", SizeBytes: 1000, StoragePath: "images/current"},
			{FileID: "history", FileName: "history.png", FileCategory: "image", DetectedMIME: "image/png", SizeBytes: 2000, StoragePath: "images/history"},
			{FileID: "sibling", FileName: "sibling.png", FileCategory: "image", DetectedMIME: "image/png", SizeBytes: 5000, StoragePath: "images/sibling"},
		}},
		anchor: ancestor, history: []model.Message{ancestor},
	}
	routes := &nativeReadinessRoutes{routes: []*channel.ResolvedRoute{{CatalogInputModalities: []string{"image"}}}}
	s := &Service{cfg: config.NewRuntime(config.Config{InputLimitsJSON: `{"defaults":{"imageCount":1,"imageSingleBytes":10}}`}), routeResolver: routes, repo: repo}
	input := NativeReadinessInput{UserID: 1, ConversationID: "chat", FileIDs: []string{"current"}}
	result, err := s.GetNativeReadiness(t.Context(), input)
	if err != nil || repo.ancestorLeaf != 0 || len(result.Issues) != 0 || !reflect.DeepEqual(result.Files, []NativeFileReadiness{{FileID: "current", Eligible: true}}) {
		t.Fatalf("without explicit parent, only current is checked and raw image size cannot prove a prepared limit: %+v (%v)", result, err)
	}
	input.ParentMessagePublicID = "chosen-parent"
	result, err = s.GetNativeReadiness(t.Context(), input)
	want := []AttachmentReadinessIssue{{Dimension: "imageCount", Actual: 2, Limit: 1, Scope: "context"}}
	if err != nil || repo.ancestorLeaf != ancestor.ID || routes.input.PlatformModelName != "saved-model" || !reflect.DeepEqual(result.Issues, want) || !reflect.DeepEqual(result.Files, []NativeFileReadiness{{FileID: "current", Reason: "budget_exceeded"}}) {
		t.Fatalf("expected only owned current and selected ancestor files: %+v (%v)", result, err)
	}
	input.FileIDs = nil
	routes.routes[0].CatalogInputModalities = []string{"text"}
	result, err = s.GetNativeReadiness(t.Context(), input)
	want = []AttachmentReadinessIssue{{Dimension: "unsupported", FileName: "history.png", FileCategory: "image", Scope: "history"}}
	if err != nil || len(result.Files) != 0 || !reflect.DeepEqual(result.Issues, want) {
		t.Fatalf("model switch without new files must still warn about unsupported files on the selected branch: %+v (%v)", result, err)
	}
	input.ParentMessagePublicID = "different-conversation"
	if _, err := s.GetNativeReadiness(t.Context(), input); !errors.Is(err, ErrInvalidMessageBranch) {
		t.Fatalf("foreign branch anchor was accepted: %v", err)
	}
}

func TestAttachmentReadinessUsesPDFMetadataWithoutReadingOrGuessingPages(t *testing.T) {
	att := nativePDF("pdf", 30, 0, true)
	policy := resolveNativeInputPolicy(&channel.ResolvedRoute{Protocol: llm.AdapterGoogleGenerateContent, CatalogInputModalities: []string{"pdf"}}, `{"defaults":{"pdfPages":10,"pdfSingleBytes":100}}`)
	reasons, issues := previewRouteAttachmentMetadata([]AttachmentInput{att}, policy, false, false)
	if reasons[att.FileID] != "unavailable" || len(issues) != 0 {
		t.Fatalf("unknown page count should not claim a proven issue: %+v %+v", reasons, issues)
	}
	att.PageCount = 11
	att.ExtractStatus = "ready"
	reasons, issues = previewRouteAttachmentMetadata([]AttachmentInput{att}, policy, false, false)
	if reasons[att.FileID] != "budget_exceeded" || len(issues) != 1 || issues[0].Dimension != "pdfPages" || issues[0].Actual != 11 {
		t.Fatalf("known original PDF over budget should report its measured page count: %+v %+v", reasons, issues)
	}
}
