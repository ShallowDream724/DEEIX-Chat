package conversation

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"testing"

	domainconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
	storageport "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/objectstorage"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/repository"
)

type moderationImageRepository struct {
	repository.ConversationRepository
	file *domainconversation.FileObject
}

func (r moderationImageRepository) GetActiveFileObjectByID(context.Context, uint, string) (*domainconversation.FileObject, error) {
	return r.file, nil
}

func TestModerationImagePreparationNeverReturnsEmptySuccess(t *testing.T) {
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var animated bytes.Buffer
	if err := gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{1, 1}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, mime string
		data       []byte
		want       error
	}{
		{"animated", "image/gif", animated.Bytes(), imageutil.ErrAnimatedImage},
		{"valid", "image/png", conversationImageFixture(t, "moderation"), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := objectstorage.NewLocal(t.TempDir())
			if _, err := store.Put(t.Context(), "input", bytes.NewReader(test.data), storageport.PutOptions{}); err != nil {
				t.Fatal(err)
			}
			s := &Service{cfg: config.NewRuntime(config.Config{ImageMaxDimension: 1536}), storeProvider: &conversationTestStoreProvider{store: store}, repo: moderationImageRepository{file: &domainconversation.FileObject{UserID: 7, FileID: "input", StoragePath: "input", MimeType: test.mime, DetectedMIME: test.mime}}}
			prepared, err := s.loadImageForModeration(t.Context(), 7, "input")
			if test.want != nil {
				if !errors.Is(err, test.want) || len(prepared.Data) != 0 {
					t.Fatalf("unsafe image accepted: %+v %v", prepared, err)
				}
			} else if err != nil || !bytes.Equal(prepared.Data, test.data) || prepared.Size != int64(len(test.data)) {
				t.Fatalf("valid image failed: %+v %v", prepared, err)
			}
		})
	}
	s := &Service{repo: moderationImageRepository{}}
	if prepared, err := s.loadImageForModeration(t.Context(), 7, "missing"); !errors.Is(err, ErrFileNotFound) || len(prepared.Data) != 0 {
		t.Fatalf("missing image returned empty success: %+v %v", prepared, err)
	}
}
