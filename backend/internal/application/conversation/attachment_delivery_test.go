package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"testing"
	"time"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/attachmentdelivery"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

type deliveryRecorder struct {
	stored, prepared, temporary, released int
	temporaryData                         []byte
}

func (*deliveryRecorder) Available() bool { return true }
func (d *deliveryRecorder) IssueStored(context.Context, uint, string, string, time.Duration) (attachmentdelivery.Link, error) {
	d.stored++
	return attachmentdelivery.Link{URL: "https://example.test/grant", SizeBytes: 400}, nil
}
func (d *deliveryRecorder) StagePrepared(_ context.Context, _ uint, _ string, data []byte, _, _, _ string) (attachmentdelivery.Link, error) {
	d.prepared++
	return attachmentdelivery.Link{URL: "https://example.test/prepared", SizeBytes: int64(len(data))}, nil
}
func (d *deliveryRecorder) StageTemporary(_ context.Context, _ uint, reader io.Reader, _, _, _ string) (attachmentdelivery.Link, error) {
	d.temporary++
	d.temporaryData, _ = io.ReadAll(reader)
	return attachmentdelivery.Link{URL: "https://example.test/temporary", SizeBytes: int64(len(d.temporaryData))}, nil
}
func (d *deliveryRecorder) ReleaseRun(context.Context, string) error { d.released++; return nil }

func TestAttachmentTransportUsesPreparedImageAndKeepsStablePrompt(t *testing.T) {
	delivery := &deliveryRecorder{}
	service := &Service{attachmentDelivery: delivery}
	data := []byte("processed image")
	stable := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartImage, Data: data, SourceFileIDs: []string{"source"}}}}}}
	before := buildPromptStateFingerprint(promptStateFingerprintInput{Messages: stable.Messages})
	sent, cleanup, err := service.prepareAttachmentTransport(t.Context(), "chat01_chat_completions", 1, stable)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	part := sent.Messages[0].Parts[0]
	sum := sha256.Sum256(data)
	if part.Reference == nil || part.Reference.SHA256 != hex.EncodeToString(sum[:]) || part.ByteSize() != int64(len(data)) || len(part.Data) != 0 || delivery.prepared != 1 || delivery.stored != 0 {
		t.Fatalf("wrong prepared delivery: %#v %#v", part, delivery)
	}
	if buildPromptStateFingerprint(promptStateFingerprintInput{Messages: stable.Messages}) != before || stable.Messages[0].Parts[0].Reference != nil {
		t.Fatal("transport modified retained prompt")
	}
}

func TestAttachmentTransportStreamsControlledTemporaryOriginal(t *testing.T) {
	path := t.TempDir() + "/file"
	if err := os.WriteFile(path, []byte("temporary original"), 0600); err != nil {
		t.Fatal(err)
	}
	delivery := &deliveryRecorder{}
	service := &Service{attachmentDelivery: delivery}
	stable := llm.GenerateInput{Messages: []llm.Message{{Role: "user", Parts: []llm.ContentPart{{Kind: llm.ContentPartAttachment, Reference: &llm.InputFileReference{FileID: "temporary_hash", TemporaryPath: path, SizeBytes: 18}}}}}}
	_, cleanup, err := service.prepareAttachmentTransport(t.Context(), "chat01_chat_completions", 1, stable)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if delivery.temporary != 1 || string(delivery.temporaryData) != "temporary original" || delivery.released != 1 {
		t.Fatalf("wrong temporary delivery: %#v", delivery)
	}
}
