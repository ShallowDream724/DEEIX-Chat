package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/deepseekfiles"
	"github.com/google/uuid"
)

const deepSeekFileTTL = 30 * 24 * time.Hour

func (c *Client) SetNativeFileStore(store portllm.NativeFileStore) { c.nativeFiles = store }
func (c *Client) SetNativeFileBilling(billing portllm.NativeFileBilling) {
	c.nativeFileBilling = billing
}

// Capability resolution happens in the application layer. This second boundary
// guards accidental activation on Pro or an unsupported transport.
func deepSeekFilesRoute(route portllm.RouteConfig, input portllm.GenerateInput) bool {
	if !input.NativeFiles || !deepseekfiles.SupportsModel(route.UpstreamModel) {
		return false
	}
	switch portllm.NormalizeAdapter(route.Protocol) {
	case portllm.AdapterDeepSeekResponses, portllm.AdapterOpenAIChatCompletions, portllm.AdapterOpenAIResponses, portllm.AdapterAnthropicMessages:
		return true
	}
	return false
}

func (c *Client) prepareDeepSeekFiles(ctx context.Context, route portllm.RouteConfig, input portllm.GenerateInput) (portllm.GenerateInput, error) {
	if !deepSeekFilesRoute(route, input) {
		return input, nil
	}
	documentCount := int64(0)
	for _, message := range input.Messages {
		for _, part := range message.Parts {
			if part.Kind == portllm.ContentPartDocument || part.Kind == portllm.ContentPartAttachment {
				documentCount++
				if part.ProviderFileID == "" {
					purpose := deepSeekFilePurpose(part)
					if purpose == "" {
						return input, errors.New("unsupported DeepSeek native file format")
					}
					limit := int64(100 * 1024 * 1024)
					if purpose == "user_data" {
						limit = 32 * 1024 * 1024
					}
					if part.ByteSize() > limit {
						return input, portllm.NewInputLimitError("provider.nativeSingleBytes", part.ByteSize(), limit)
					}
					if part.PageCount > 1000 {
						return input, portllm.NewInputLimitError("provider.pdfPages", int64(part.PageCount), 1000)
					}
					if len(part.Data) == 0 || part.ByteSize() != int64(len(part.Data)) {
						return input, errors.New("native file source unavailable or incomplete")
					}
				}
			}
		}
	}
	if documentCount > 20 {
		return input, portllm.NewInputLimitError("provider.nativeCount", documentCount, 20)
	}
	cloned := false
	for mi, msg := range input.Messages {
		for pi, part := range msg.Parts {
			if (part.Kind != portllm.ContentPartDocument && part.Kind != portllm.ContentPartAttachment) || part.ProviderFileID != "" {
				continue
			}
			if input.UserID == 0 || c.nativeFiles == nil {
				return input, errors.New("native file storage or authenticated user unavailable")
			}
			if !cloned {
				input.Messages = append([]portllm.Message(nil), input.Messages...)
				cloned = true
			}
			input.Messages[mi].Parts = append([]portllm.ContentPart(nil), input.Messages[mi].Parts...)
			purpose := deepSeekFilePurpose(part)
			if purpose == "" {
				return input, errors.New("unsupported DeepSeek native file format")
			}
			if len(part.Data) == 0 || part.ByteSize() != int64(len(part.Data)) {
				return input, errors.New("native file source unavailable or incomplete")
			}
			maxBytes := int64(100 * 1024 * 1024)
			if purpose == "user_data" {
				maxBytes = 32 * 1024 * 1024
			}
			if int64(len(part.Data)) > maxBytes || part.PageCount > 1000 {
				return input, errors.New("DeepSeek file exceeds provider limit")
			}
			id, err := c.ensureDeepSeekFile(ctx, route, input.UserID, part, purpose)
			if err != nil {
				return input, err
			}
			part.ProviderFileID = id
			// Keep bytes/metadata through final request validation. Encoders use ID.
			input.Messages[mi].Parts[pi] = part
		}
	}
	return input, nil
}

func deepSeekFilePurpose(part portllm.ContentPart) string {
	if format, ok := deepseekfiles.Resolve(part.FileName, part.MimeType); ok {
		return format.Purpose
	}
	return ""
}

func digestNativeFile(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (c *Client) ensureDeepSeekFile(ctx context.Context, route portllm.RouteConfig, userID uint, part portllm.ContentPart, purpose string) (string, error) {
	source := ""
	if len(part.SourceFileIDs) > 0 {
		source = part.SourceFileIDs[0]
	}
	sum := sha256.Sum256(part.Data)
	scope := digestNativeFile(deepSeekFilesURL(route.BaseURL), route.APIKey, route.HeadersJSON)
	key := digestNativeFile(strconv.FormatUint(uint64(userID), 10), scope, source, hex.EncodeToString(sum[:]), purpose, "v1")
	for {
		record, found, err := c.nativeFiles.Load(ctx, key)
		if err != nil {
			return "", err
		}
		if found && (record.UserID != userID || record.Scope != scope || record.SizeBytes != int64(len(part.Data)) || record.Purpose != purpose) {
			return "", errors.New("native file cache identity mismatch")
		}
		now := time.Now()
		if found && record.State == "ready" && record.ExpiresAt.After(now.Add(time.Minute)) {
			if !record.BillingSettled {
				if err := c.completeNativeFileBilling(ctx, &record); err != nil {
					return "", err
				}
			}
			return record.ProviderFileID, nil
		}
		if found && record.LeaseUntil.After(now) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(150 * time.Millisecond):
				continue
			}
		}
		claim := uuid.NewString()
		if !found {
			record = portllm.NativeFileRecord{Key: key, UserID: userID, Scope: scope, SourceID: source, Purpose: purpose, FileName: "deeix_" + key + filepath.Ext(part.FileName), SizeBytes: int64(len(part.Data)), State: "pending", CreatedAt: now, ExpiresAt: now.Add(deepSeekFileTTL)}
		}
		record.ClaimToken, record.LeaseUntil = claim, now.Add(5*time.Minute)
		claimed, err := c.nativeFiles.Claim(ctx, record)
		if err != nil {
			return "", err
		}
		if !claimed {
			continue
		}
		if found && record.State != "failed" && record.ExpiresAt.After(now) {
			// A prior process may have disconnected after upstream accepted the
			// upload. Reconcile by a private deterministic filename, never retry it.
			remote, recoveryErr := c.findDeepSeekFile(ctx, route, record)
			if recoveryErr == nil && remote.ID != "" {
				record.ProviderFileID, record.State, record.LeaseUntil = remote.ID, "ready", time.Time{}
				if remote.ExpiresAt > 0 {
					record.ExpiresAt = time.Unix(remote.ExpiresAt, 0)
				}
				if err = c.nativeFiles.Save(ctx, record); err != nil {
					return "", err
				}
				if err = c.completeNativeFileBilling(ctx, &record); err != nil {
					return "", err
				}
				return remote.ID, nil
			}
			record.State, record.LeaseUntil = "uncertain", time.Time{}
			_ = c.nativeFiles.Save(context.WithoutCancel(ctx), record)
			if recoveryErr != nil {
				return "", fmt.Errorf("native file recovery failed: %w", recoveryErr)
			}
			return "", errors.New("native file upload outcome is unresolved; retry later to reconcile without uploading again")
		}
		// Expiry is explicit and bounded; replacing an expired file is a new
		// parse. The unique suffix prevents finding an old generation on recovery.
		if found {
			record.FileName = "deeix_" + key + "_" + claim + filepath.Ext(part.FileName)
		}
		record.State, record.ExpiresAt = "pending", now.Add(deepSeekFileTTL)
		record.Pages, record.QuantityKnown = int64(part.PageCount), part.PageCount > 0
		if purpose == "user_data" {
			record.Pages, record.QuantityKnown = 0, true
		}
		record.BillingSettled = false
		if c.nativeFileBilling != nil {
			record.BillingTicketJSON, err = c.nativeFileBilling.Prepare(ctx, record, record.Pages, record.QuantityKnown)
			if err != nil {
				record.State, record.LeaseUntil = "failed", time.Time{}
				_ = c.nativeFiles.Save(context.WithoutCancel(ctx), record)
				return "", err
			}
		}
		if err = c.nativeFiles.Save(ctx, record); err != nil {
			return "", err
		}
		remote, uploadErr := c.uploadDeepSeekFile(ctx, route, record, part.Data)
		if uploadErr != nil {
			record.State, record.LeaseUntil = "uncertain", time.Time{}
			var rejected *nativeFileRejectedError
			if errors.As(uploadErr, &rejected) {
				record.State = "failed"
			}
			_ = c.nativeFiles.Save(context.WithoutCancel(ctx), record)
			return "", uploadErr
		}
		record.ProviderFileID, record.State, record.LeaseUntil = remote.ID, "ready", time.Time{}
		if remote.ExpiresAt > 0 {
			record.ExpiresAt = time.Unix(remote.ExpiresAt, 0)
		}
		if err = c.nativeFiles.Save(context.WithoutCancel(ctx), record); err != nil {
			return "", err
		}
		if err = c.completeNativeFileBilling(ctx, &record); err != nil {
			return "", err
		}
		return remote.ID, nil
	}
}

func (c *Client) completeNativeFileBilling(ctx context.Context, record *portllm.NativeFileRecord) error {
	if c.nativeFileBilling != nil {
		if err := c.nativeFileBilling.Complete(context.WithoutCancel(ctx), *record, record.Pages, record.QuantityKnown); err != nil {
			return err
		}
	}
	record.BillingSettled = true
	return c.nativeFiles.Save(context.WithoutCancel(ctx), *record)
}

type deepSeekRemoteFile struct {
	ID        string `json:"id"`
	FileName  string `json:"filename"`
	Purpose   string `json:"purpose"`
	Bytes     int64  `json:"bytes"`
	ExpiresAt int64  `json:"expires_at"`
}
type nativeFileRejectedError struct{ err error }

func (e *nativeFileRejectedError) Error() string { return e.err.Error() }
func (e *nativeFileRejectedError) Unwrap() error { return e.err }

func deepSeekFilesURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/anthropic/v1")
	base = strings.TrimSuffix(base, "/anthropic")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/files"
}

func (c *Client) deepSeekFileRequest(ctx context.Context, route portllm.RouteConfig, method, target, contentType string, body io.Reader) ([]byte, int, error) {
	var req *http.Request
	var err error
	if body == nil {
		req, err = http.NewRequestWithContext(ctx, method, target, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, target, body)
	}
	if err != nil {
		return nil, 0, err
	}
	if sized, ok := body.(interface{ Len() int }); ok {
		req.ContentLength = int64(sized.Len())
	}
	req.Header.Set("Authorization", "Bearer "+route.APIKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	setAdditionalHeaders(req, route.HeadersJSON)
	resp, err := c.doRouteRequest(route, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := readUpstreamBody(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, parseUpstreamError(resp.StatusCode, raw, nil)
	}
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, resp.StatusCode, errors.New("invalid native file response")
	}
	if len(asMap(envelope["error"])) > 0 {
		return nil, resp.StatusCode, parseUpstreamError(http.StatusBadGateway, raw, nil)
	}
	return raw, resp.StatusCode, nil
}

type deepSeekUploadBody struct {
	io.Reader
	length int
}

func (b deepSeekUploadBody) Len() int { return b.length }

func (c *Client) uploadDeepSeekFile(ctx context.Context, route portllm.RouteConfig, record portllm.NativeFileRecord, data []byte) (deepSeekRemoteFile, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("purpose", record.Purpose)
	_ = writer.WriteField("expires_after[anchor]", "created_at")
	_ = writer.WriteField("expires_after[seconds]", strconv.FormatInt(int64(deepSeekFileTTL/time.Second), 10))
	_, err := writer.CreateFormFile("file", record.FileName)
	if err != nil {
		return deepSeekRemoteFile{}, err
	}
	headerLength := body.Len()
	_ = writer.Close()
	// Stream the existing source between small multipart envelopes. Copying the
	// whole document into another buffer doubles peak memory for large uploads.
	length := body.Len() + len(data)
	if limit := portllm.EffectiveInputMediaLimits(route).RequestBytes; limit > 0 && int64(length) > limit {
		return deepSeekRemoteFile{}, &nativeFileRejectedError{errors.New("native upload exceeds request byte budget")}
	}
	uploadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	envelope := body.Bytes()
	upload := deepSeekUploadBody{Reader: io.MultiReader(bytes.NewReader(envelope[:headerLength]), bytes.NewReader(data), bytes.NewReader(envelope[headerLength:])), length: length}
	raw, status, err := c.deepSeekFileRequest(uploadCtx, route, http.MethodPost, deepSeekFilesURL(route.BaseURL), writer.FormDataContentType(), upload)
	if err != nil {
		if status >= 400 && status < 500 {
			return deepSeekRemoteFile{}, &nativeFileRejectedError{err}
		}
		return deepSeekRemoteFile{}, err
	}
	var result deepSeekRemoteFile
	if json.Unmarshal(raw, &result) != nil || result.ID == "" {
		return result, errors.New("native file response lacks an ID")
	}
	return result, nil
}

func (c *Client) findDeepSeekFile(ctx context.Context, route portllm.RouteConfig, record portllm.NativeFileRecord) (deepSeekRemoteFile, error) {
	after := ""
	for page := 0; page < 10; page++ {
		query := url.Values{"limit": {"1000"}, "purpose": {record.Purpose}}
		if after != "" {
			query.Set("after", after)
		}
		raw, _, err := c.deepSeekFileRequest(ctx, route, http.MethodGet, deepSeekFilesURL(route.BaseURL)+"?"+query.Encode(), "", nil)
		if err != nil {
			return deepSeekRemoteFile{}, err
		}
		var list struct {
			Data    []deepSeekRemoteFile `json:"data"`
			HasMore bool                 `json:"has_more"`
			LastID  string               `json:"last_id"`
		}
		if err = json.Unmarshal(raw, &list); err != nil {
			return deepSeekRemoteFile{}, err
		}
		for _, file := range list.Data {
			if file.FileName == record.FileName && file.Bytes == record.SizeBytes && file.Purpose == record.Purpose && (file.ExpiresAt == 0 || file.ExpiresAt > time.Now().Unix()+60) {
				return file, nil
			}
		}
		if !list.HasMore {
			return deepSeekRemoteFile{}, nil
		}
		if list.LastID == "" || list.LastID == after {
			return deepSeekRemoteFile{}, errors.New("invalid native file pagination")
		}
		after = list.LastID
	}
	return deepSeekRemoteFile{}, errors.New("native file recovery listing limit reached")
}
