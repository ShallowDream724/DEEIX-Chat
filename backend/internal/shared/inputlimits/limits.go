// Package inputlimits defines the administrator-controlled budgets shared by
// conversation planning and the final provider request transport.
package inputlimits

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Limits are effective values. Zero disables that additional budget. Byte
// budgets use bytes, including decimal provider limits where applicable.
type Limits struct {
	MessageFileCount int `json:"messageFileCount"`
	// Attachment budgets combine images and original files in the full request.
	AttachmentCount       int   `json:"attachmentCount"`
	AttachmentBytes       int64 `json:"attachmentBytes"`
	AttachmentSingleBytes int64 `json:"attachmentSingleBytes"`
	RequestBytes          int64 `json:"requestBytes"`
	ImageCount            int   `json:"imageCount"`
	ImageBytes            int64 `json:"imageBytes"`
	// ImageSingleBytes limits one prepared image before Base64 encoding.
	ImageSingleBytes int64 `json:"imageSingleBytes"`
	NativeCount      int   `json:"nativeCount"`
	NativeBytes      int64 `json:"nativeBytes"`
	PDFSingleBytes   int64 `json:"pdfSingleBytes"`
	PDFBytes         int64 `json:"pdfBytes"`
	PDFPages         int   `json:"pdfPages"`
	NonPDFBytes      int64 `json:"nonPDFBytes"`
}

type overrides struct {
	MessageFileCount      *int   `json:"messageFileCount,omitempty"`
	AttachmentCount       *int   `json:"attachmentCount,omitempty"`
	AttachmentBytes       *int64 `json:"attachmentBytes,omitempty"`
	AttachmentSingleBytes *int64 `json:"attachmentSingleBytes,omitempty"`
	RequestBytes          *int64 `json:"requestBytes,omitempty"`
	ImageCount            *int   `json:"imageCount,omitempty"`
	ImageBytes            *int64 `json:"imageBytes,omitempty"`
	ImageSingleBytes      *int64 `json:"imageSingleBytes,omitempty"`
	NativeCount           *int   `json:"nativeCount,omitempty"`
	NativeBytes           *int64 `json:"nativeBytes,omitempty"`
	PDFSingleBytes        *int64 `json:"pdfSingleBytes,omitempty"`
	PDFBytes              *int64 `json:"pdfBytes,omitempty"`
	PDFPages              *int   `json:"pdfPages,omitempty"`
	NonPDFBytes           *int64 `json:"nonPDFBytes,omitempty"`
}

type configuration struct {
	Defaults  *overrides            `json:"defaults"`
	Upstreams map[string]*overrides `json:"upstreams"`
}

func defaults() Limits {
	return Limits{
		MessageFileCount: 10, RequestBytes: 100_000_000,
		ImageCount: 20, ImageSingleBytes: 5_000_000, ImageBytes: 20_000_000,
		NativeCount: 10, NativeBytes: 50_000_000, PDFSingleBytes: 50_000_000,
		PDFBytes: 50_000_000, PDFPages: 200, NonPDFBytes: 50_000_000,
	}
}

// ResolveMessageFileCount uses the legacy setting only without a valid budget.
// Stored legacy values are migrated into defaults.messageFileCount on startup.
func ResolveMessageFileCount(rawJSON, protocol, baseURL string, upstreamID uint, fallback int) int {
	if fallback <= 0 {
		fallback = defaults().MessageFileCount
	}
	cfg, err := decode(rawJSON)
	if err != nil {
		return fallback
	}
	fallback = defaults().MessageFileCount
	if cfg.Defaults != nil && cfg.Defaults.MessageFileCount != nil {
		fallback = *cfg.Defaults.MessageFileCount
	}
	if upstream := cfg.Upstreams[strconv.FormatUint(uint64(upstreamID), 10)]; upstream != nil && upstream.MessageFileCount != nil {
		fallback = *upstream.MessageFileCount
	}
	return fallback
}

// DefaultConfigJSON exposes one complete administrator budget and flat upstream
// overrides. The request budget is an application default, not a provider limit.
func DefaultConfigJSON() string {
	raw, _ := json.Marshal(struct {
		Defaults  Limits            `json:"defaults"`
		Upstreams map[string]Limits `json:"upstreams"`
	}{defaults(), map[string]Limits{}})
	return string(raw)
}

// Materialize returns effective defaults while retaining partial upstream
// overrides. It does not change the persisted configuration.
func Materialize(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return DefaultConfigJSON()
	}
	cfg, err := decode(raw)
	if err != nil {
		return raw
	}
	value := defaults()
	apply(&value, cfg.Defaults)
	if cfg.Upstreams == nil {
		cfg.Upstreams = map[string]*overrides{}
	}
	result, _ := json.Marshal(struct {
		Defaults  Limits                `json:"defaults"`
		Upstreams map[string]*overrides `json:"upstreams"`
	}{value, cfg.Upstreams})
	return string(result)
}

// Resolve applies one system budget, then one flat upstream override. Protocol
// and base URL are retained for callers but cannot select administrator budgets.
// Invalid persisted input falls back to defaults; Validate rejects it on save.
func Resolve(rawJSON, _, _ string, upstreamID uint) Limits {
	result := defaults()
	if strings.TrimSpace(rawJSON) == "" {
		return result
	}
	cfg, err := decode(rawJSON)
	if err != nil {
		return result
	}
	apply(&result, cfg.Defaults)
	if upstreamID > 0 {
		apply(&result, cfg.Upstreams[strconv.FormatUint(uint64(upstreamID), 10)])
	}
	return result
}

// Validate accepts only defaults and flat upstream fields, nonnegative whole values,
// and canonical positive upstream IDs. Null fields inherit their parent value.
func Validate(raw string) error {
	_, err := decode(raw)
	return err
}

func decode(raw string) (configuration, error) {
	var cfg configuration
	if trimmed := strings.TrimSpace(raw); trimmed == "" || trimmed[0] != '{' {
		return cfg, fmt.Errorf("input limits must be a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid input limits: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return cfg, fmt.Errorf("input limits must contain one JSON object")
	}
	// encoding/json otherwise matches struct fields case-insensitively. Settings
	// accept only the documented camelCase keys, so spelling errors cannot turn
	// into silently different effective budgets.
	var root map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &root)
	allowed := map[string]bool{"requestBytes": true, "imageCount": true, "imageBytes": true, "imageSingleBytes": true, "nativeCount": true, "nativeBytes": true, "pdfSingleBytes": true, "pdfBytes": true, "pdfPages": true, "nonPDFBytes": true}
	allowed["messageFileCount"] = true
	allowed["attachmentCount"] = true
	allowed["attachmentBytes"] = true
	allowed["attachmentSingleBytes"] = true
	validateFields := func(path string, raw json.RawMessage) error {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return fmt.Errorf("%s must be an object", path)
		}
		for field := range fields {
			if !allowed[field] {
				return fmt.Errorf("unknown input limits field %s.%s", path, field)
			}
		}
		return nil
	}
	for scope, values := range root {
		if scope == "defaults" {
			if err := validateFields(scope, values); err != nil {
				return cfg, err
			}
			continue
		}
		if scope != "upstreams" {
			return cfg, fmt.Errorf("unknown input limits field %q", scope)
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(values, &entries); err != nil || entries == nil {
			return cfg, fmt.Errorf("upstreams must be an object")
		}
		for key, entry := range entries {
			if err := validateFields(scope+"."+key, entry); err != nil {
				return cfg, err
			}
		}
	}
	if cfg.Defaults != nil {
		if err := validateOverride("defaults", cfg.Defaults); err != nil {
			return cfg, err
		}
	}
	for key, value := range cfg.Upstreams {
		id, err := strconv.ParseUint(key, 10, strconv.IntSize)
		if err != nil || id == 0 || strconv.FormatUint(id, 10) != key {
			return cfg, fmt.Errorf("invalid input limits upstream ID %q", key)
		}
		if value == nil {
			return cfg, fmt.Errorf("upstreams.%s must be an object", key)
		}
		if err := validateOverride("upstreams."+key, value); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func validateOverride(path string, value *overrides) error {
	if value == nil {
		return fmt.Errorf("%s must be an object", path)
	}
	raw, _ := json.Marshal(value)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for key, rawValue := range fields {
		if bytes.Equal(rawValue, []byte("null")) {
			continue
		}
		var number int64
		_ = json.Unmarshal(rawValue, &number)
		max := int64(9_007_199_254_740_991) // largest exact integer in the settings UI
		if key == "messageFileCount" || key == "attachmentCount" || key == "imageCount" || key == "nativeCount" || key == "pdfPages" {
			max = 2_147_483_647
		}
		if number < 0 || number > max {
			return fmt.Errorf("%s.%s must be between 0 and %d", path, key, max)
		}
	}
	return nil
}

func apply(dst *Limits, src *overrides) {
	if src == nil {
		return
	}
	if src.MessageFileCount != nil {
		dst.MessageFileCount = *src.MessageFileCount
	}
	if src.AttachmentCount != nil {
		dst.AttachmentCount = *src.AttachmentCount
	}
	if src.AttachmentBytes != nil {
		dst.AttachmentBytes = *src.AttachmentBytes
	}
	if src.AttachmentSingleBytes != nil {
		dst.AttachmentSingleBytes = *src.AttachmentSingleBytes
	}
	if src.RequestBytes != nil {
		dst.RequestBytes = *src.RequestBytes
	}
	if src.ImageCount != nil {
		dst.ImageCount = *src.ImageCount
	}
	if src.ImageBytes != nil {
		dst.ImageBytes = *src.ImageBytes
	}
	if src.ImageSingleBytes != nil {
		dst.ImageSingleBytes = *src.ImageSingleBytes
	}
	if src.NativeCount != nil {
		dst.NativeCount = *src.NativeCount
	}
	if src.NativeBytes != nil {
		dst.NativeBytes = *src.NativeBytes
	}
	if src.PDFSingleBytes != nil {
		dst.PDFSingleBytes = *src.PDFSingleBytes
	}
	if src.PDFBytes != nil {
		dst.PDFBytes = *src.PDFBytes
	}
	if src.PDFPages != nil {
		dst.PDFPages = *src.PDFPages
	}
	if src.NonPDFBytes != nil {
		dst.NonPDFBytes = *src.NonPDFBytes
	}
}
