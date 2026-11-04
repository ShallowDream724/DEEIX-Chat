// Package inputlimits defines the administrator-controlled budgets shared by
// conversation planning and the final provider request transport.
package inputlimits

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// Limits are effective values. Zero disables that additional budget. Byte
// budgets use bytes, including decimal provider limits where applicable.
type Limits struct {
	RequestBytes int64 `json:"requestBytes"`
	ImageCount   int   `json:"imageCount"`
	ImageBytes   int64 `json:"imageBytes"`
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
	RequestBytes     *int64 `json:"requestBytes,omitempty"`
	ImageCount       *int   `json:"imageCount,omitempty"`
	ImageBytes       *int64 `json:"imageBytes,omitempty"`
	ImageSingleBytes *int64 `json:"imageSingleBytes,omitempty"`
	NativeCount      *int   `json:"nativeCount,omitempty"`
	NativeBytes      *int64 `json:"nativeBytes,omitempty"`
	PDFSingleBytes   *int64 `json:"pdfSingleBytes,omitempty"`
	PDFBytes         *int64 `json:"pdfBytes,omitempty"`
	PDFPages         *int   `json:"pdfPages,omitempty"`
	NonPDFBytes      *int64 `json:"nonPDFBytes,omitempty"`
}

type configuration struct {
	Profiles  map[string]*overrides         `json:"profiles"`
	Upstreams map[string]*upstreamOverrides `json:"upstreams"`
}

type upstreamOverrides struct {
	overrides
	Protocols map[string]*overrides `json:"protocols,omitempty"`
}

var supportedProtocols = map[string]bool{
	"openai_responses": true, "openai_chat_completions": true,
	"openai_image_generations": true, "openai_image_edits": true,
	"openrouter_chat_completions": true, "openrouter_responses": true, "openrouter_images": true,
	"anthropic_messages": true, "google_generate_content": true, "google_image_generation": true, "gemini_interactions": true,
	"xai_responses": true, "xai_image": true, "xai_image_edits": true, "xai_video": true, "xai_video_extensions": true,
}

func defaults() map[string]Limits {
	compatible := Limits{RequestBytes: 100_000_000, NativeBytes: 14 * 1024 * 1024, PDFSingleBytes: 8 * 1024 * 1024, PDFBytes: 8 * 1024 * 1024, PDFPages: 100, NonPDFBytes: 14 * 1024 * 1024}
	anthropic := Limits{RequestBytes: 128_000_000, ImageCount: 600, PDFPages: 600}
	xaiEdits, xaiVideo, openrouterImages := compatible, compatible, compatible
	xaiEdits.ImageCount, xaiVideo.ImageCount, openrouterImages.ImageCount = 3, 1, 16
	return map[string]Limits{
		"openai":                  {RequestBytes: 512_000_000, ImageCount: 1500, NativeBytes: 50_000_000, PDFSingleBytes: 49_999_999, PDFBytes: 50_000_000, NonPDFBytes: 14 * 1024 * 1024},
		"google_generate_content": {RequestBytes: 100_000_000, PDFSingleBytes: 50_000_000, PDFPages: 200},
		"gemini_interactions":     {RequestBytes: 100_000_000},
		"anthropic":               anthropic,
		"openrouter":              compatible,
		"compatible":              compatible,
		"xai_image_edits":         xaiEdits,
		"xai_video":               xaiVideo,
		"openrouter_images":       openrouterImages,
	}
}

// DefaultConfigJSON exposes complete effective profile values to settings UIs.
// Compatible/OpenRouter requestBytes is an application default, not a provider guarantee.
func DefaultConfigJSON() string {
	raw, _ := json.Marshal(struct {
		Profiles  map[string]Limits `json:"profiles"`
		Upstreams map[string]Limits `json:"upstreams"`
	}{defaults(), map[string]Limits{}})
	return string(raw)
}

// Materialize returns all effective profile values while retaining partial
// upstream overrides, so settings clients can display inherited values without
// duplicating provider defaults. It does not change the persisted configuration.
func Materialize(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return DefaultConfigJSON()
	}
	cfg, err := decode(raw)
	if err != nil {
		return raw
	}
	profiles := defaults()
	for key, override := range cfg.Profiles {
		value := profiles[key]
		apply(&value, override)
		profiles[key] = value
	}
	if cfg.Upstreams == nil {
		cfg.Upstreams = map[string]*upstreamOverrides{}
	}
	result, _ := json.Marshal(struct {
		Profiles  map[string]Limits             `json:"profiles"`
		Upstreams map[string]*upstreamOverrides `json:"upstreams"`
	}{profiles, cfg.Upstreams})
	return string(result)
}

// Profile selects the budget profile without assuming OpenAI compatibility means
// the official OpenAI service. A protocol's capabilities remain a separate concern.
func Profile(protocol, baseURL string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case "xai_image_edits", "xai_video", "openrouter_images":
		return protocol
	case "google_generate_content", "google_image_generation", "gemini_generate_content":
		return "google_generate_content"
	case "gemini_interactions":
		return "gemini_interactions"
	case "openrouter_chat_completions", "openrouter_responses":
		return "openrouter"
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil {
		host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
		if host == "api.anthropic.com" && protocol == "anthropic_messages" {
			return "anthropic"
		}
		if host == "openrouter.ai" {
			return "openrouter"
		}
		if host == "api.openai.com" && (protocol == "" || protocol == "openai_responses" || protocol == "openai_chat_completions") {
			return "openai"
		}
	}
	return "compatible"
}

// Resolve applies defaults, then profile overrides, then upstream overrides.
// Invalid persisted input falls back to defaults; Validate rejects it on save.
func Resolve(rawJSON, protocol, baseURL string, upstreamID uint) Limits {
	profile := Profile(protocol, baseURL)
	result := defaults()[profile]
	if strings.TrimSpace(rawJSON) == "" {
		return result
	}
	cfg, err := decode(rawJSON)
	if err != nil {
		return result
	}
	apply(&result, cfg.Profiles[profile])
	if upstreamID > 0 {
		if upstream := cfg.Upstreams[strconv.FormatUint(uint64(upstreamID), 10)]; upstream != nil {
			apply(&result, &upstream.overrides)
			protocol = strings.ToLower(strings.TrimSpace(protocol))
			if protocol == "" {
				protocol = "openai_responses"
			}
			apply(&result, upstream.Protocols[protocol])
		}
	}
	return result
}

// Validate accepts only known profiles and fields, nonnegative whole values,
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
	for scope, values := range root {
		if scope != "profiles" && scope != "upstreams" {
			return cfg, fmt.Errorf("unknown input limits field %q", scope)
		}
		var entries map[string]json.RawMessage
		_ = json.Unmarshal(values, &entries)
		for key, entry := range entries {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(entry, &fields)
			for field, fieldRaw := range fields {
				if scope == "upstreams" && field == "protocols" {
					var protocols map[string]map[string]json.RawMessage
					if err := json.Unmarshal(fieldRaw, &protocols); err != nil {
						return cfg, fmt.Errorf("%s.%s.protocols must be an object", scope, key)
					}
					for protocol, protocolFields := range protocols {
						if !supportedProtocols[protocol] {
							return cfg, fmt.Errorf("unknown input limits protocol %q", protocol)
						}
						for name := range protocolFields {
							if !allowed[name] {
								return cfg, fmt.Errorf("unknown input limits field %s.%s.protocols.%s.%s", scope, key, protocol, name)
							}
						}
					}
					continue
				}
				if !allowed[field] {
					return cfg, fmt.Errorf("unknown input limits field %s.%s.%s", scope, key, field)
				}
			}
		}
	}
	known := defaults()
	for key, value := range cfg.Profiles {
		if _, ok := known[key]; !ok {
			return cfg, fmt.Errorf("unknown input limits profile %q", key)
		}
		if err := validateOverride("profiles."+key, value); err != nil {
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
		if err := validateOverride("upstreams."+key, &value.overrides); err != nil {
			return cfg, err
		}
		for protocol, override := range value.Protocols {
			if err := validateOverride("upstreams."+key+".protocols."+protocol, override); err != nil {
				return cfg, err
			}
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
		if key == "imageCount" || key == "nativeCount" || key == "pdfPages" {
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
