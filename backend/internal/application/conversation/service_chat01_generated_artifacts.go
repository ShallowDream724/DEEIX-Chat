package conversation

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

// Partial failures remain internal metadata and visible ordinary Markdown. No
// phantom file IDs or new attachment payload types are sent to the frontend.
type generatedArtifactFailure struct {
	ID          string
	FileName    string
	SourceLinks []string
	Cause       error
}

type chat01GeneratedArtifact struct {
	file  llm.GeneratedFile
	image bool
}

func chat01ArtifactBatch(input assistantGeneratedFilesInput) []chat01GeneratedArtifact {
	var result []chat01GeneratedArtifact
	byURL := make(map[string]int)
	for _, file := range mergeAssistantGeneratedFiles(nil, input.Files) {
		if len(file.SourceLinks) == 1 {
			byURL[file.SourceLinks[0]] = len(result)
		}
		result = append(result, chat01GeneratedArtifact{file: file, image: chat01ImageFileName(file.FileName)})
	}
	for _, image := range input.Images {
		raw := strings.TrimSpace(image.URL)
		digest := sha256.Sum256([]byte(raw))
		id := "chat01-image-url:" + hex.EncodeToString(digest[:])
		if i, exists := byURL[raw]; exists {
			result[i].image = true
			result[i].file.ID = id
			continue
		}
		name := "generated-image.png"
		if u, err := url.Parse(raw); err == nil && path.Base(u.Path) != "." && path.Base(u.Path) != "/" {
			name = path.Base(u.Path)
		}
		byURL[raw] = len(result)
		result = append(result, chat01GeneratedArtifact{file: llm.GeneratedFile{ID: id, FileName: name, SourceLinks: []string{raw}}, image: true})
	}
	return result
}

// saveChat01GeneratedArtifacts uses one batch of outcomes for files and images.
// Each UploadFile uses the existing quota-row transaction and content deduplication;
// there is no per-item quota exemption and no separate all-or-nothing image batch.
func (s *Service) saveChat01GeneratedArtifacts(ctx context.Context, input assistantGeneratedFilesInput) (result *assistantGeneratedFilesResult, retErr error) {
	artifacts := chat01ArtifactBatch(input)
	if len(artifacts) == 0 {
		return nil, nil
	}
	result = &assistantGeneratedFilesResult{Content: input.Content}
	reader, available := s.llmClient.(llm.GeneratedFileReader)
	created := make([]string, 0, len(artifacts))
	defer func() {
		if retErr != nil {
			s.cleanupGeneratedFiles(ctx, input.UserID, created)
		}
	}()
	links := make(map[string]string)
	seenFiles := make(map[string]bool)
	imageCount := 0
	for _, artifact := range artifacts {
		if ctx.Err() != nil {
			return nil, ErrMessageGenerationCanceled
		}
		file := artifact.file
		var saved *appupload.UploadFileResult
		var err error
		if !available || s.uploadSvc == nil || strings.TrimSpace(file.ID) == "" || strings.TrimSpace(file.FileName) == "" {
			err = ErrGeneratedFileDownloadFailed
		} else if countErr := s.validateMessageFileCount(make([]string, len(result.AttachmentRows)+1)); countErr != nil {
			err = countErr
		} else if artifact.image && imageCount >= maxAssistantImageContentItems {
			err = ErrTooManyMessageFiles
		} else {
			saved, err = s.saveChat01GeneratedArtifact(ctx, input, reader, artifact, imageCount)
		}
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrMessageGenerationCanceled) {
				return nil, ErrMessageGenerationCanceled
			}
			result.Failures = append(result.Failures, generatedArtifactFailure{ID: file.ID, FileName: file.FileName, SourceLinks: append([]string(nil), file.SourceLinks...), Cause: err})
			continue
		}
		f := saved.File
		if !saved.Reused {
			created = append(created, f.FileID)
		}
		target := "/api/v1/files/" + f.FileID + "/content"
		for _, source := range file.SourceLinks {
			if previous, exists := links[source]; exists && previous != target {
				links[source] = ""
			} else if !exists {
				links[source] = target
			}
		}
		if seenFiles[f.FileID] {
			continue
		}
		seenFiles[f.FileID] = true
		kind := inferAttachmentKind(f.DetectedMIME)
		if kind == "image" {
			imageCount++
		}
		result.AttachmentRows = append(result.AttachmentRows, model.Attachment{ConversationID: input.ConversationID, MessageID: input.AssistantMessageID, UserID: input.UserID, FileID: f.FileID, Kind: kind, FileName: f.FileName, MimeType: f.DetectedMIME, FileSize: f.SizeBytes, SHA256: f.SHA256, StoragePath: f.StoragePath, Status: "active", UploadedAt: time.Now()})
		snapshot := attachmentsFromFiles([]model.FileObject{f})[0]
		snapshot.Kind = kind
		result.AttachmentSnapshots = append(result.AttachmentSnapshots, snapshot)
	}
	content, represented := rewriteChat01ArtifactLinks(input.Content, links)
	for _, row := range result.AttachmentRows {
		if row.Kind != "image" {
			continue
		}
		target := "/api/v1/files/" + row.FileID + "/content"
		if !represented[target] {
			content = appendArtifactMarkdown(content, "!["+artifactMarkdownName(row.FileName)+"]("+target+")")
		}
	}
	for _, failure := range result.Failures {
		content = appendArtifactMarkdown(content, artifactFailureMarkdown(failure))
	}
	result.Content = content
	result.CreatedFileIDs = created
	return result, nil
}

func (s *Service) saveChat01GeneratedArtifact(ctx context.Context, input assistantGeneratedFilesInput, reader llm.GeneratedFileReader, artifact chat01GeneratedArtifact, imageCount int) (*appupload.UploadFileResult, error) {
	content, err := reader.OpenGeneratedFile(ctx, input.Route, artifact.file)
	if err != nil {
		return nil, s.generatedFileFailure(ctx, input, "download", err)
	}
	if content == nil || content.Body == nil {
		return nil, ErrGeneratedFileDownloadFailed
	}
	defer content.Body.Close()
	buffered := bufio.NewReaderSize(content.Body, 512)
	prefix, _ := buffered.Peek(12)
	detected := detectGeneratedImageMIME(prefix)
	upload := appupload.UploadFileInput{UserID: input.UserID, Purpose: "generated_file", FileName: artifact.file.FileName, MimeType: content.MimeType, DeclaredSize: content.SizeBytes, Reader: buffered}
	if artifact.image || strings.HasPrefix(strings.ToLower(content.MimeType), "image/") || detected != "" {
		if imageCount >= maxAssistantImageContentItems {
			return nil, ErrTooManyMessageFiles
		}
		if detected == "" {
			return nil, s.generatedFileFailure(ctx, input, "validation", fmt.Errorf("invalid generated image content"))
		}
		upload.Purpose = "generated_image"
		upload.MimeType = detected
		upload.Reader = buffered
		upload.MaxBytes = s.generatedImageMaxBytes()
	}
	saved, err := s.uploadSvc.UploadFile(ctx, upload)
	if err != nil {
		if errors.Is(err, ErrFileTooLarge) || errors.Is(err, ErrStorageQuotaExceeded) || errors.Is(err, ErrMIMEBlocked) || errors.Is(err, ErrDangerousMIMEType) {
			return nil, err
		}
		return nil, s.generatedFileFailure(ctx, input, "save", err)
	}
	return saved, nil
}

func chat01ImageFileName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg", ".bmp", ".avif":
		return true
	}
	return false
}

// Replace only whole, adapter-confirmed URLs. Longer URLs and ordinary sources
// cannot inherit another artifact's identity by sharing its prefix.
func rewriteChat01ArtifactLinks(content string, links map[string]string) (string, map[string]bool) {
	keys := make([]string, 0, len(links))
	for source, target := range links {
		if source != "" && target != "" {
			keys = append(keys, source)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	represented := make(map[string]bool)
	if len(keys) == 0 {
		return content, represented
	}
	patterns := make([]string, len(keys))
	for i, key := range keys {
		patterns[i] = regexp.QuoteMeta(key)
	}
	re := regexp.MustCompile(strings.Join(patterns, "|"))
	var out strings.Builder
	last := 0
	for _, span := range re.FindAllStringIndex(content, -1) {
		if span[0] > 0 && !artifactURLBoundary(content[span[0]-1]) {
			continue
		}
		if span[1] < len(content) && !artifactURLBoundary(content[span[1]]) {
			continue
		}
		source := content[span[0]:span[1]]
		target := links[source]
		out.WriteString(content[last:span[0]])
		out.WriteString(target)
		last = span[1]
		represented[target] = true
	}
	out.WriteString(content[last:])
	return out.String(), represented
}
func artifactURLBoundary(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || strings.ContainsRune("()<>\"'`[]", rune(c))
}
func artifactMarkdownName(name string) string {
	name = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\r", " ", "\n", " ").Replace(name)
	if strings.TrimSpace(name) == "" {
		return "生成文件"
	}
	return name
}
func appendArtifactMarkdown(content, line string) string {
	if strings.TrimSpace(content) == "" {
		return line
	}
	return strings.TrimRight(content, "\r\n") + "\n\n" + line
}
func artifactFailureMarkdown(f generatedArtifactFailure) string {
	name := artifactMarkdownName(f.FileName)
	if len(f.SourceLinks) == 1 {
		if u, err := url.Parse(f.SourceLinks[0]); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && !strings.ContainsAny(f.SourceLinks[0], "<>\r\n") {
			return "[" + name + "](<" + f.SourceLinks[0] + ">)（保存失败）"
		}
	}
	return name + "（保存失败）"
}
