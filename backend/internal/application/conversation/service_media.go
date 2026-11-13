package conversation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/filetype"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/pkg/imageutil"
)

const maxMediaImageEditInputPixels = 64 * 1024 * 1024

func resolveImageMimeType(mimeType string) string {
	return imageutil.ResolveMimeType(mimeType)
}

func prepareImageData(ctx context.Context, data []byte, mime string, maxDim int, userID uint) (preparedConversationImage, error) {
	prepared, actual, err := imageutil.Prepare(ctx, data, mime, imageutil.ProcessingPolicy{MaxDimension: maxDim}, userID)
	if err != nil {
		return preparedConversationImage{}, err
	}
	return preparedConversationImage{data: prepared, mimeType: actual}, nil
}

func attachmentImagePreparationError(att AttachmentInput, err error) error {
	reason := attachmentInputReadFailed
	if errors.Is(err, imageutil.ErrAnimatedImage) {
		reason = "unsupported_animation"
		err = fmt.Errorf("%w: %w", ErrUnsupportedImageAnimation, err)
	} else if errors.Is(err, imageutil.ErrInvalidImage) || errors.Is(err, imageutil.ErrImagePixelLimit) || errors.Is(err, imageutil.ErrImageDecodeBudget) {
		reason = "image_processing_failed"
		err = fmt.Errorf("%w: %w", ErrImageProcessingFailed, err)
	}
	return attachmentInputErrorWithReason(att, err, reason)
}

// normalizeMediaImageEditInput 将用户上传的编辑输入图规整为静态 PNG。
// 手机拍摄图片常带有上游不稳定支持的编码、色彩模式或容器元数据；图片编辑协议统一接收这里输出的 8-bit RGBA PNG。
func normalizeMediaImageEditInput(data []byte, declaredMIME string) ([]byte, string, error) {
	return normalizeMediaImageEditInputContext(context.Background(), data, declaredMIME, 0)
}

func normalizeMediaImageEditInputContext(ctx context.Context, data []byte, declaredMIME string, userID uint) ([]byte, string, error) {
	return imageutil.Prepare(ctx, data, declaredMIME, imageutil.ProcessingPolicy{ForcePNG: true, MaxPixels: maxMediaImageEditInputPixels}, userID)
}

func mediaImageEditInputFileName(fileName string, mimeType string) string {
	normalizedName := strings.TrimSpace(fileName)
	ext := filepath.Ext(normalizedName)
	base := strings.TrimSuffix(normalizedName, ext)
	if strings.TrimSpace(base) == "" {
		base = "image-edit-input"
	}
	return base + filetype.ImageExtension(mimeType)
}
