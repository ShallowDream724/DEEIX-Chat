package upload

import (
	"math"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
)

const defaultUploadBytes int64 = config.DefaultMaxUploadFileBytes

// EffectiveMaxBytes resolves a category override, falling back to the default.
// Callers must use the detected content type for final file validation.
func EffectiveMaxBytes(cfg config.Config, mimeType, fileName string) int64 {
	if limit := maxBytesForCategory(inferFileCategory(mimeType, fileName), cfg); limit > 0 {
		return limit
	}
	if cfg.MaxUploadFileBytes > 0 {
		return cfg.MaxUploadFileBytes
	}
	return defaultUploadBytes
}

// MaxUploadBytes bounds an upload before its content category is known.
func MaxUploadBytes(cfg config.Config) int64 {
	limit := cfg.MaxUploadFileBytes
	if limit <= 0 {
		limit = defaultUploadBytes
	}
	for _, override := range []int64{cfg.FileImageMaxBytes, cfg.FileDocMaxBytes, cfg.FileAudioMaxBytes, cfg.FileVideoMaxBytes} {
		if override > limit {
			limit = override
		}
	}
	return limit
}

// MaxUploadRequestBytes includes multipart fields and saturates on overflow.
func MaxUploadRequestBytes(cfg config.Config, fileCount int64, overheads ...int64) int64 {
	var overhead int64
	for _, value := range overheads {
		if value <= 0 {
			continue
		}
		if value > math.MaxInt64-overhead {
			return math.MaxInt64
		}
		overhead += value
	}
	if fileCount <= 0 {
		return overhead
	}
	limit := MaxUploadBytes(cfg)
	if limit > (math.MaxInt64-overhead)/fileCount {
		return math.MaxInt64
	}
	return limit*fileCount + overhead
}
