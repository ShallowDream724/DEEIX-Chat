package upload

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

func TestUploadWAVCanonicalizesDetectedSignatureBeforeAllowlist(t *testing.T) {
	audio := make([]byte, 44+640)
	copy(audio, "RIFF")
	binary.LittleEndian.PutUint32(audio[4:], uint32(len(audio)-8))
	copy(audio[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(audio[16:], 16)
	binary.LittleEndian.PutUint16(audio[20:], 1)
	binary.LittleEndian.PutUint16(audio[22:], 1)
	binary.LittleEndian.PutUint32(audio[24:], 16000)
	binary.LittleEndian.PutUint32(audio[28:], 32000)
	binary.LittleEndian.PutUint16(audio[32:], 2)
	binary.LittleEndian.PutUint16(audio[34:], 16)
	copy(audio[36:], "data")
	binary.LittleEndian.PutUint32(audio[40:], 640)
	for _, fileName := range []string{"voice.wav", "renamed.txt"} {
		t.Run(fileName, func(t *testing.T) {
			cfg := config.Config{MaxUploadFileBytes: 1 << 20, UserStorageQuotaBytes: 10 << 20, FileAllowedMIMETypes: "audio/wav"}
			service := NewServiceWithRuntime(config.NewRuntime(cfg), newUploadTestRepo(), nil, Hooks{}, ErrorSet{}, "test")
			service.SetObjectStoreProvider(uploadTestStoreProvider{store: newUploadTestStore()})
			result, err := service.UploadFile(context.Background(), UploadFileInput{UserID: 1, Purpose: "chat", FileName: fileName, MimeType: "audio/wave", DeclaredSize: int64(len(audio)), Reader: bytes.NewReader(audio)})
			if err != nil {
				t.Fatal(err)
			}
			if result.File.DetectedMIME != "audio/wav" || result.File.MimeType != "audio/wave" || result.File.FileCategory != fileCategoryAudio {
				t.Fatalf("unexpected WAV metadata: declared=%s detected=%s category=%s", result.File.MimeType, result.File.DetectedMIME, result.File.FileCategory)
			}
		})
	}
}

func TestWAVAllowlistAcceptsEquivalentMIMENames(t *testing.T) {
	for _, alias := range []string{"audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave"} {
		cfg := config.Config{FileAllowedMIMETypes: alias}
		if !isAllowedMIME("audio/wav", cfg) || !isAllowedMIME(alias, config.Config{FileAllowedMIMETypes: "audio/wav"}) {
			t.Fatalf("WAV alias %q was rejected", alias)
		}
		if isAllowedMIME("audio/mpeg", cfg) {
			t.Fatalf("WAV allowlist admitted another audio format")
		}
	}
}
