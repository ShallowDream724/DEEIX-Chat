package speech

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	domain "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/speech"
)

func TestDeepSeekASRUploadPreservesMultipartAndLength(t *testing.T) {
	audio := bytes.Repeat([]byte{0, 1, 0xff, 0x20}, 8192)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/asr" || r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("incorrect ASR route or authentication")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || r.ContentLength != int64(len(body)) || r.Header.Get("Content-Length") != strconv.Itoa(len(body)) || len(r.TransferEncoding) != 0 {
			t.Errorf("incorrect request body framing: length=%d read=%d err=%v", r.ContentLength, len(body), err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		parts, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		part, err := parts.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		data, err := io.ReadAll(part)
		if err != nil || part.FormName() != "file" || part.FileName() != "speech.wav" || !bytes.Equal(data, audio) {
			t.Errorf("audio multipart changed: name=%q filename=%q err=%v", part.FormName(), part.FileName(), err)
		}
		if _, err = parts.NextPart(); err != io.EOF {
			t.Errorf("expected multipart footer, got %v", err)
		}
		_, _ = io.WriteString(w, `{"text":"完整录音","duration_ms":1000,"request_id":"test-request"}`)
	}))
	defer server.Close()
	provider := NewDeepSeek(ProviderConfig{BaseURL: server.URL, APIKey: "test-only"}, server.Client())
	result, err := provider.Transcribe(context.Background(), domain.Audio{Bytes: audio, Format: "wav"})
	if err != nil || result.Text != "完整录音" || result.DurationMS != 1000 || !result.Final {
		t.Fatalf("unexpected transcription: %+v, %v", result, err)
	}
}

type asrDiscardTransport struct{}

func (asrDiscardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"text":"ok","duration_ms":120000}`)), Header: make(http.Header)}, nil
}

func BenchmarkDeepSeekASRUpload(b *testing.B) {
	// A full 120-second PCM16 recording is allocated before measurement.
	audio := domain.Audio{Bytes: make([]byte, 120*16000*2), Format: "wav"}
	provider := NewDeepSeek(ProviderConfig{APIKey: "test-only"}, &http.Client{Transport: asrDiscardTransport{}})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := provider.Transcribe(context.Background(), audio); err != nil {
			b.Fatal(err)
		}
	}
}
