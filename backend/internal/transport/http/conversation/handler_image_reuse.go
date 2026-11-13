package conversation

import (
	"errors"
	"net/http"

	appconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/conversation"
	appupload "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/upload"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

type ImageSourceFingerprintRequest struct {
	SourceSHA256       string `json:"sourceSha256"`
	SourceSizeBytes    int64  `json:"sourceSizeBytes"`
	ImagePolicyVersion string `json:"imagePolicyVersion"`
}
type LookupImageReuseRequest struct {
	Items []ImageSourceFingerprintRequest `json:"items" binding:"required,max=100"`
}
type ImageReuseItemResponse struct {
	SourceSHA256       string              `json:"sourceSha256"`
	SourceSizeBytes    int64               `json:"sourceSizeBytes"`
	ImagePolicyVersion string              `json:"imagePolicyVersion"`
	File               *FileObjectResponse `json:"file" extensions:"x-nullable,!x-omitempty"`
}
type LookupImageReuseResponse struct {
	Items              []ImageReuseItemResponse `json:"items"`
	ImagePolicyVersion string                   `json:"imagePolicyVersion"`
	Quota              StorageQuotaResponse     `json:"quota"`
}

type LookupImageReuseResponseDoc struct {
	ErrorMsg string                   `json:"errorMsg"`
	Data     LookupImageReuseResponse `json:"data"`
}

// LookupImageReuse godoc
// @Summary 按当前用户原始图片指纹批量查找可复用文件
// @Tags chat
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body LookupImageReuseRequest true "图片来源指纹"
// @Success 200 {object} LookupImageReuseResponseDoc
// @Failure 400 {object} ErrorDoc
// @Router /files/image-reuse/lookup [post]
func (h *Handler) LookupImageReuse(c *gin.Context) {
	var input LookupImageReuseRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.ErrorFrom(c, http.StatusBadRequest, errInvalidFile)
		return
	}
	userID := middleware.MustUserID(c)
	sources := make([]appupload.ImageSourceFingerprint, len(input.Items))
	for i, item := range input.Items {
		sources[i] = appupload.ImageSourceFingerprint{SourceSHA256: item.SourceSHA256, SourceSizeBytes: item.SourceSizeBytes, ImagePolicyVersion: item.ImagePolicyVersion}
	}
	files, err := h.uploads.LookupImageSources(c.Request.Context(), userID, sources)
	if err != nil {
		if errors.Is(err, appconversation.ErrInvalidFileReference) {
			response.ErrorFrom(c, http.StatusBadRequest, errInvalidFile)
			return
		}
		response.InternalError(c, err)
		return
	}
	quota, err := h.uploads.GetStorageQuota(c.Request.Context(), userID)
	if err != nil {
		response.InternalError(c, err)
		return
	}
	fileList := make([]model.FileObject, 0, len(files))
	for _, file := range files {
		fileList = append(fileList, file)
	}
	capabilities := h.processing.ResolveFileVectorizationCapabilities(c.Request.Context(), fileList)
	items := make([]ImageReuseItemResponse, len(sources))
	for i, source := range sources {
		items[i] = ImageReuseItemResponse{SourceSHA256: source.SourceSHA256, SourceSizeBytes: source.SourceSizeBytes, ImagePolicyVersion: source.ImagePolicyVersion}
		if file, ok := files[source]; ok {
			dto := toFileObjectResponse(&file, capabilities[file.FileID])
			items[i].File = &dto
		}
	}
	response.Success(c, LookupImageReuseResponse{Items: items, ImagePolicyVersion: appupload.ImagePolicyVersion(h.cfg.Snapshot().ImageMaxDimension), Quota: toStorageQuotaResponse(*quota)})
}
