package embedding

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/extraction"
	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
	storageport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
)

type imageEmbeddingStoreProvider struct{ store storageport.Store }

func (p imageEmbeddingStoreProvider) Open(context.Context) (storageport.Store, error) {
	return p.store, nil
}

func embeddingImageService(t *testing.T, data []byte, mime string, maxDimension int) (*Service, domainconversation.FileObject) {
	t.Helper()
	store := objectstorage.NewLocal(t.TempDir())
	if _, err := store.Put(t.Context(), "input", bytes.NewReader(data), storageport.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	cfg := config.NewRuntime(config.Config{ImageMaxDimension: maxDimension})
	extractSvc := extraction.NewServiceWithRuntime(cfg, extraction.EngineFactories{})
	extractSvc.SetObjectStoreProvider(imageEmbeddingStoreProvider{store: store})
	return &Service{cfg: cfg, extractSvc: extractSvc}, domainconversation.FileObject{UserID: 7, StoragePath: "input", MimeType: mime, DetectedMIME: mime, FileCategory: "image"}
}

func TestEmbeddingImagePreparationRejectsSmallAnimationInvalidAndResourceExcess(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	large := append([]byte(nil), encoded.Bytes()...)
	binary.BigEndian.PutUint32(large[16:20], 4097)
	binary.BigEndian.PutUint32(large[20:24], 4097)
	binary.BigEndian.PutUint32(large[29:33], crc32.ChecksumIEEE(large[12:29]))
	for _, test := range []struct {
		name, mime string
		data       []byte
		want       error
	}{
		{"animated below byte limit", "image/gif", animated.Bytes(), imageutil.ErrAnimatedImage},
		{"invalid below byte limit", "image/png", []byte("invalid"), imageutil.ErrInvalidImage},
		{"invalid above byte limit", "image/png", bytes.Repeat([]byte{0xab}, maxImageEmbeddingBytes+1), imageutil.ErrInvalidImage},
		{"resource excess below byte limit", "image/png", large, imageutil.ErrImageDecodeBudget},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, file := embeddingImageService(t, test.data, test.mime, 1536)
			data, _, err := s.loadImageForEmbedding(t.Context(), file)
			if !errors.Is(err, test.want) || len(data) != 0 {
				t.Fatalf("unsafe input must fail before embedding: bytes=%d error=%v", len(data), err)
			}
		})
	}
}

func TestEmbeddingImagePreparationPreservesFastPathAndDimensionZero(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 4, 2))); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range []int{0, 1536, 2} {
		s, file := embeddingImageService(t, source.Bytes(), "image/png", dimension)
		data, mime, err := s.loadImageForEmbedding(t.Context(), file)
		if err != nil || mime != "image/png" || len(data) == 0 {
			t.Fatalf("prepare: %s %v", mime, err)
		}
		if dimension != 2 && !bytes.Equal(data, source.Bytes()) {
			t.Fatal("conforming embedding image was re-encoded")
		}
		if dimension == 2 {
			metadata, err := imageutil.Inspect(data)
			if err != nil || metadata.Width != 2 {
				t.Fatalf("configured dimension ignored: %+v %v", metadata, err)
			}
		}
	}
}

func TestEmbeddingImagePreparationRetainsHardByteFallback(t *testing.T) {
	im := image.NewNRGBA(image.Rect(0, 0, 1536, 1536))
	state := uint32(0xabcdef01)
	for i := range im.Pix {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		im.Pix[i] = byte(state)
	}
	var source bytes.Buffer
	if err := png.Encode(&source, im); err != nil {
		t.Fatal(err)
	}
	if source.Len() <= maxImageEmbeddingBytes {
		t.Fatal("fixture does not exercise hard byte fallback")
	}
	s, file := embeddingImageService(t, source.Bytes(), "image/png", 0)
	data, mime, err := s.loadImageForEmbedding(t.Context(), file)
	if err != nil || len(data) == 0 || len(data) > maxImageEmbeddingBytes || mime != "image/png" {
		t.Fatalf("hard byte fallback failed: bytes=%d MIME=%s err=%v", len(data), mime, err)
	}
	metadata, err := imageutil.Inspect(data)
	if err != nil || metadata.Width >= 1536 {
		t.Fatalf("fallback did not shrink image: %+v %v", metadata, err)
	}
}
