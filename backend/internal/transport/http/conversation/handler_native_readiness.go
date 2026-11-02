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

type NativeReadinessRequest struct {
	Model          string   `json:"model" binding:"max=128"`
	FileIDs        []string `json:"fileIDs" binding:"required,min=1,max=100,dive,required,max=128"`
	ConversationID string   `json:"conversationID,omitempty" binding:"omitempty,max=32"`
}

// GetNativeReadiness reports native file eligibility without choosing a billing
// route or waiting for background extraction.
// @Summary 查询本轮附件原生输入资格
// @Tags chat
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body NativeReadinessRequest true "模型与本轮全部附件"
// @Success 200 {object} appconversation.NativeReadinessResult
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
	})
	if err != nil {
		switch {
		case errors.Is(err, apppersonalprovider.ErrModelUnavailable):
			response.ErrorFrom(c, http.StatusServiceUnavailable, err)
		case errors.Is(err, appconversation.ErrModelAccessDenied):
			response.ErrorFrom(c, http.StatusForbidden, err)
		case errors.Is(err, appconversation.ErrConversationNotFound):
			response.ErrorFrom(c, http.StatusNotFound, err)
		case errors.Is(err, appconversation.ErrInvalidFileReference), errors.Is(err, appconversation.ErrTooManyMessageFiles), errors.Is(err, appconversation.ErrModelRouteNotConfigured):
			response.ErrorFrom(c, http.StatusBadRequest, err)
		default:
			response.InternalError(c, err)
		}
		return
	}
	response.Success(c, result)
}
