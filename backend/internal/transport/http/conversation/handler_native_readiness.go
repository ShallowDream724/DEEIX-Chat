package conversation

import (
	"errors"
	"net/http"

	appconversation "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/conversation"
	apppersonalprovider "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/personalprovider"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
)

// GetNativeReadiness previews attachment limits and eligibility using trusted metadata.
// @Summary 查询所选模型的附件输入限制与当前分支附件状态
// @Tags chat
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body NativeReadinessRequest true "模型、当前分支父消息及本轮附件"
// @Success 200 {object} NativeReadinessResponseDoc
// @Failure 400 {object} ErrorDoc
// @Failure 403 {object} ErrorDoc
// @Failure 404 {object} ErrorDoc
// @Failure 503 {object} ErrorDoc
// @Router /files/native-readiness [post]
func (h *Handler) GetNativeReadiness(c *gin.Context) {
	var req NativeReadinessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidRequestBody(c, err)
		return
	}
	result, err := h.service.GetNativeReadiness(c.Request.Context(), appconversation.NativeReadinessInput{
		UserID: middleware.MustUserID(c), Model: req.Model, FileIDs: req.FileIDs, ConversationID: req.ConversationID,
		ParentMessagePublicID: req.ParentMessagePublicID, SelectedToolIDs: req.SelectedToolIDs,
	})
	if err != nil {
		switch {
		case errors.Is(err, apppersonalprovider.ErrModelUnavailable):
			response.ErrorFrom(c, http.StatusServiceUnavailable, err)
		case errors.Is(err, appconversation.ErrModelAccessDenied):
			response.ErrorFrom(c, http.StatusForbidden, err)
		case errors.Is(err, appconversation.ErrConversationNotFound):
			response.ErrorFrom(c, http.StatusNotFound, err)
		case errors.Is(err, appconversation.ErrInvalidFileReference), errors.Is(err, appconversation.ErrInvalidMessageBranch), errors.Is(err, appconversation.ErrModelRouteNotConfigured):
			response.ErrorFrom(c, http.StatusBadRequest, err)
		default:
			response.InternalError(c, err)
		}
		return
	}
	response.Success(c, toNativeReadinessResponse(result))
}
