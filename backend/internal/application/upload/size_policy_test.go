package upload

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"math"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestUploadSizePolicyOverridesAndRequestCeiling(t *testing.T) {
	cfg := config.Config{MaxUploadFileBytes: 20, FileImageMaxBytes: 10, FileDocMaxBytes: 50, FileAudioMaxBytes: 30, FileVideoMaxBytes: 40}
	for _, tc := range []struct {
		mime string
		want int64
	}{
		{"image/png", 10}, {"text/plain", 50}, {"audio/mpeg", 30}, {"video/mp4", 40},
	} {
		if got := EffectiveMaxBytes(cfg, tc.mime, ""); got != tc.want {
			t.Errorf("%s limit = %d, want %d", tc.mime, got, tc.want)
		}
	}
	if got := EffectiveMaxBytes(config.Config{MaxUploadFileBytes: 20}, "audio/mpeg", ""); got != 20 {
		t.Fatalf("audio should inherit the default, got %d", got)
	}
	if got := MaxUploadRequestBytes(cfg, 3, 7); got != 157 {
		t.Fatalf("request ceiling = %d, want 157", got)
	}
	cfg.FileDocMaxBytes = math.MaxInt64
	if got := MaxUploadRequestBytes(cfg, 3, 7); got != math.MaxInt64 {
		t.Fatalf("overflow ceiling = %d", got)
	}
	for _, limit := range []int64{0, -1} {
		cfg.FileDocMaxBytes = limit
		if got := EffectiveMaxBytes(cfg, "text/plain", ""); got != 20 {
			t.Fatalf("unset override = %d, want default 20", got)
		}
	}
}

func TestCategoryOverrideAppliesToPersistentAndTemporaryUploads(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, temporary := range []bool{false, true} {
		for _, tc := range []struct {
			name, mime string
			content    []byte
			wantOK     bool
		}{
			{"notes.txt", "text/plain", bytes.Repeat([]byte("a"), 30), true},
			{"notes.txt", "image/png", bytes.Repeat([]byte("a"), 30), true},
			{"voice.mp3", "audio/mpeg", bytes.Repeat([]byte("a"), 30), true},
			{"clip.mp4", "video/mp4", bytes.Repeat([]byte("a"), 40), true},
			{"large.mp3", "audio/mpeg", bytes.Repeat([]byte("a"), 31), false},
			{"large.mp4", "video/mp4", bytes.Repeat([]byte("a"), 41), false},
			{"image.png", "image/png", encoded.Bytes(), false},
			{"disguised.pdf", "application/pdf", encoded.Bytes(), false},
			{"disguised.mp4", "video/mp4", encoded.Bytes(), false},
			{"large.txt", "text/plain", bytes.Repeat([]byte("a"), 121), false},
		} {
			t.Run(tc.name+"/"+tc.mime+map[bool]string{false: "/persistent", true: "/temporary"}[temporary], func(t *testing.T) {
				store := newUploadTestStore()
				service := newUploadTestService(newUploadTestRepo(), store)
				cfg := service.cfg.Snapshot()
				cfg.MaxUploadFileBytes, cfg.FileImageMaxBytes, cfg.FileDocMaxBytes = 20, 10, 120
				cfg.FileAudioMaxBytes, cfg.FileVideoMaxBytes = 30, 40
				service.cfg.Store(cfg)
				var err error
				if temporary {
					var file *PreparedTemporaryFile
					file, err = service.PrepareTemporaryFile(context.Background(), TemporaryFileInput{
						FileName: tc.name, MimeType: tc.mime, Reader: bytes.NewReader(tc.content), DeclaredSize: int64(len(tc.content)),
					})
					if file != nil {
						defer file.Cleanup()
					}
				} else {
					_, err = service.UploadFile(context.Background(), UploadFileInput{
						UserID: 1, FileName: tc.name, MimeType: tc.mime, Reader: bytes.NewReader(tc.content), DeclaredSize: int64(len(tc.content)),
					})
				}
				if (err == nil) != tc.wantOK {
					t.Fatalf("upload error = %v, wantOK %v", err, tc.wantOK)
				}
				if !tc.wantOK && store.objectCount() != 0 {
					t.Fatal("rejected upload reached object storage")
				}
			})
		}
	}
}
