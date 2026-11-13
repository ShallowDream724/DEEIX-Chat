// Package imageutil 提供与业务无关的图片缩放与 MIME 归一化。
package imageutil

import (
	_ "image/gif" // 注册 GIF 解码器。
	"slices"
	"strings"

	_ "golang.org/x/image/webp" // 注册 WebP 解码器。
)

// supportedMimeTypes 是本包能解码并且主流多模态接口接受的图片格式。
var supportedMimeTypes = []string{"image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp"}

// SupportedMimeTypes 返回 IsSupportedMimeType 接受的全部 MIME，供需要在查询中表达同一范围的调用方使用。
func SupportedMimeTypes() []string {
	return append([]string(nil), supportedMimeTypes...)
}

// IsSupportedMimeType 判断 MIME 是否为本包能解码并且主流多模态接口接受的图片格式。
func IsSupportedMimeType(mimeType string) bool {
	return slices.Contains(supportedMimeTypes, strings.ToLower(strings.TrimSpace(mimeType)))
}

// ResolveMimeType 规范化图片 MIME 类型，未知时默认为 image/jpeg。
func ResolveMimeType(mimeType string) string {
	normalized := strings.ToLower(strings.TrimSpace(mimeType))
	switch normalized {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
		return normalized
	default:
		return "image/jpeg"
	}
}
