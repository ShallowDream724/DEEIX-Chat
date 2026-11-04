package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/inputlimits"
)

func TestInputMediaBudgetsCountMixedAndRepeatedParts(t *testing.T) {
	pdf := portllm.ContentPart{Kind: portllm.ContentPartDocument, Data: []byte("pdf"), PageCount: 2}
	input := portllm.GenerateInput{Messages: []portllm.Message{{Parts: []portllm.ContentPart{
		{Kind: portllm.ContentPartImage, Data: []byte("image")}, pdf,
		{Kind: portllm.ContentPartAudio, Data: []byte("audio")},
	}}, {Parts: []portllm.ContentPart{pdf, {Kind: portllm.ContentPartVideo, Data: []byte("video")}}}}}
	boundary := inputlimits.Limits{ImageCount: 1, ImageBytes: 5, ImageSingleBytes: 5, NativeCount: 4, NativeBytes: 16, PDFSingleBytes: 3, PDFBytes: 6, PDFPages: 4, NonPDFBytes: 10}
	if err := validateInputMedia(portllm.RouteConfig{InputLimits: &boundary}, input); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		limits inputlimits.Limits
	}{
		{"imageBytes", inputlimits.Limits{ImageBytes: 4}},
		{"imageSingleBytes", inputlimits.Limits{ImageSingleBytes: 4}},
		{"nativeCount", inputlimits.Limits{NativeCount: 3}},
		{"nativeBytes", inputlimits.Limits{NativeBytes: 15}},
		{"pdfSingleBytes", inputlimits.Limits{PDFSingleBytes: 2}},
		{"pdfBytes", inputlimits.Limits{PDFBytes: 5}},
		{"pdfPages", inputlimits.Limits{PDFPages: 3}},
		{"nonPDFBytes", inputlimits.Limits{NonPDFBytes: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateInputMedia(portllm.RouteConfig{InputLimits: &tc.limits}, input)
			if !errors.Is(err, portllm.ErrInputTooLarge) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("want %s limit: %v", tc.name, err)
			}
		})
	}
	input.Messages = append(input.Messages, portllm.Message{Parts: []portllm.ContentPart{{Kind: portllm.ContentPartImage, Data: []byte("image")}}})
	if err := validateInputMedia(portllm.RouteConfig{InputLimits: &boundary}, input); !errors.Is(err, portllm.ErrInputTooLarge) || !strings.Contains(err.Error(), "imageCount") {
		t.Fatalf("repeated image not counted: %v", err)
	}
}

func TestEncodedRequestBudgetBoundary(t *testing.T) {
	limits := inputlimits.Limits{RequestBytes: 100_000_000}
	route := portllm.RouteConfig{InputLimits: &limits}
	for _, size := range []int64{0, 99_999_999, 100_000_000} {
		if err := validateEncodedRequestSize(route, size); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateEncodedRequestSize(route, 100_000_001); !errors.Is(err, portllm.ErrInputTooLarge) {
		t.Fatalf("want limit error: %v", err)
	}
	if err := validateEncodedRequestSize(route, -1); !errors.Is(err, portllm.ErrInputTooLarge) {
		t.Fatalf("unknown length bypassed budget: %v", err)
	}
	limits.RequestBytes = 0
	if err := validateEncodedRequestSize(route, 100_000_001); err != nil {
		t.Fatalf("zero budget should disable restriction: %v", err)
	}
}

func TestAllChatProtocolsRejectEncodedRequestBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newTestClient()
	defer client.CloseIdleConnections()
	for _, protocol := range []string{portllm.AdapterOpenAIResponses, portllm.AdapterOpenAIChatCompletions, portllm.AdapterOpenRouterChat, portllm.AdapterOpenRouterResponses, portllm.AdapterAnthropicMessages, portllm.AdapterGoogleGenerateContent, portllm.AdapterGeminiInteractions} {
		for _, stream := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/generate", true: "/stream"}[stream], func(t *testing.T) {
				limits := inputlimits.Limits{RequestBytes: 10}
				route := portllm.RouteConfig{Protocol: protocol, BaseURL: server.URL, UpstreamModel: "test-model", InputLimits: &limits}
				input := portllm.GenerateInput{Messages: []portllm.Message{{Role: "user", Content: "hello"}}, Options: map[string]any{"temperature": 0.5}}
				var err error
				if stream {
					_, err = client.GenerateStream(context.Background(), route, input, func(portllm.GenerateStreamEvent) error { return nil })
				} else {
					_, err = client.Generate(context.Background(), route, input)
				}
				if !errors.Is(err, portllm.ErrInputTooLarge) || portllm.RequestWasAccepted(err) {
					t.Fatalf("want unaccepted input error: %v", err)
				}
			})
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("sent %d oversized requests", got)
	}
}

func TestMediaBudgetStopsGenerateAndStreamBeforeAdapter(t *testing.T) {
	client := newTestClient()
	limits := inputlimits.Limits{ImageCount: 1}
	route := portllm.RouteConfig{Protocol: portllm.AdapterOpenAIResponses, InputLimits: &limits}
	image := portllm.ContentPart{Kind: portllm.ContentPartImage, Data: []byte("image")}
	input := portllm.GenerateInput{Messages: []portllm.Message{{Role: "user", Parts: []portllm.ContentPart{image, image}}}}
	if _, err := client.Generate(context.Background(), route, input); !errors.Is(err, portllm.ErrInputTooLarge) {
		t.Fatalf("generate: %v", err)
	}
	if _, err := client.GenerateStream(context.Background(), route, input, nil); !errors.Is(err, portllm.ErrInputTooLarge) {
		t.Fatalf("stream: %v", err)
	}
}

func TestImageGenerationRejectsEncodedJSONAndMultipartBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	client := newTestClient()
	defer client.CloseIdleConnections()
	for _, protocol := range []string{portllm.AdapterOpenAIImageGenerations, portllm.AdapterOpenAIImageEdits, portllm.AdapterGoogleImageGeneration} {
		for _, stream := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/json", true: "/stream"}[stream], func(t *testing.T) {
				limits := inputlimits.Limits{RequestBytes: 1}
				route := portllm.RouteConfig{Protocol: protocol, BaseURL: server.URL, UpstreamModel: "gpt-image-1", InputLimits: &limits}
				input := portllm.GenerateInput{Messages: []portllm.Message{{Role: "user", Content: "draw", Parts: []portllm.ContentPart{{Kind: portllm.ContentPartText, Text: "draw"}, {Kind: portllm.ContentPartImage, MimeType: "image/png", FileName: "reference.png", Data: []byte("image")}}}}}
				var err error
				if stream {
					_, err = client.GenerateStream(context.Background(), route, input, func(portllm.GenerateStreamEvent) error { return nil })
				} else {
					_, err = client.Generate(context.Background(), route, input)
				}
				if !errors.Is(err, portllm.ErrInputTooLarge) || portllm.RequestWasAccepted(err) {
					t.Fatalf("want unaccepted encoded budget rejection: %v", err)
				}
			})
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized generation sent %d requests", calls.Load())
	}
}

func TestEncodedPayloadBoundaryIncludesRepeatedMediaTextToolsAndOptions(t *testing.T) {
	imagePart := portllm.ContentPart{Kind: portllm.ContentPartImage, MimeType: "image/png", Data: []byte("image")}
	pdfPart := portllm.ContentPart{Kind: portllm.ContentPartDocument, MimeType: "application/pdf", FileName: "file.pdf", Data: []byte("pdf")}
	input := portllm.GenerateInput{
		Messages: []portllm.Message{{Role: "user", Parts: []portllm.ContentPart{{Kind: portllm.ContentPartText, Text: "question"}, imagePart, pdfPart}}, {Role: "user", Parts: []portllm.ContentPart{imagePart, pdfPart}}},
		Tools:    []portllm.ToolDefinition{{Name: "lookup", Description: "lookup a record", InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)}},
		Options:  map[string]any{"temperature": 0.5, "metadata": map[string]any{"label": strings.Repeat("context", 20)}},
	}
	requestBody, err := buildOpenAIRequestBody(portllm.AdapterOpenAIChatCompletions, "test-model", portllm.EndpointChatCompletions, input, false)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.ContentLength != int64(len(payload)) {
			t.Errorf("actual payload length %d, expected %d", request.ContentLength, len(payload))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	client := newTestClient()
	defer client.CloseIdleConnections()
	limits := inputlimits.Limits{RequestBytes: int64(len(payload) - 1)}
	route := portllm.RouteConfig{Protocol: portllm.AdapterOpenAIChatCompletions, BaseURL: server.URL, UpstreamModel: "test-model", InputLimits: &limits}
	if _, err := client.Generate(context.Background(), route, input); !errors.Is(err, portllm.ErrInputTooLarge) || calls.Load() != 0 {
		t.Fatalf("one-byte overage reached upstream: %v calls=%d", err, calls.Load())
	}
	limits.RequestBytes++
	if _, err := client.Generate(context.Background(), route, input); err != nil || calls.Load() != 1 {
		t.Fatalf("exact serialized boundary rejected: %v calls=%d", err, calls.Load())
	}
}
