package conversation

import (
	"errors"
	app "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/conversation"
	model "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/conversation"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/response"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/transport/http/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
)

// SaveMessageRevision godoc
// @Summary 保存用户消息的新版本
// @Description 新建包含完整附件集合的用户消息分支，不请求模型，保留原消息与后续回答
// @Tags chat
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "原用户消息 public_id"
// @Param body body SaveMessageRevisionRequest true "消息新版本"
// @Success 200 {object} MessageRevisionResponseDoc
// @Failure 400 {object} ErrorDoc
// @Failure 404 {object} ErrorDoc
// @Failure 409 {object} ErrorDoc
// @Router /messages/{id}/revisions [post]
func (h *Handler) SaveMessageRevision(c *gin.Context) {
	id, err := stringParam(c, "id")
	if err != nil {
		response.ErrorFrom(c, http.StatusBadRequest, errInvalidMessageID)
		return
	}
	var req SaveMessageRevisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidRequestBody(c, err)
		return
	}
	item, err := h.service.SaveUserMessageRevision(c.Request.Context(), middleware.MustUserID(c), id, req.Content, req.FileIDs, req.ExpectedUpdatedAt, req.RevisionID)
	if err != nil {
		switch {
		case errors.Is(err, app.ErrMessageNotFound):
			response.ErrorFrom(c, http.StatusNotFound, err)
		case errors.Is(err, app.ErrMessageEditStateInvalid):
			response.ErrorFrom(c, http.StatusConflict, err)
		case errors.Is(err, app.ErrInvalidMessageContent), errors.Is(err, app.ErrMessageEditTargetInvalid), errors.Is(err, app.ErrInvalidFileReference):
			response.ErrorFrom(c, http.StatusBadRequest, err)
		default:
			response.InternalError(c, err)
		}
		return
	}
	h.recordAudit(c, "save_message_revision", "message", item.PublicID, map[string]any{"sourceMessagePublicID": id})
	response.Success(c, MessageRevisionResponse{Message: toMessageResponseWithRun(*item, model.Run{}), SelectedLeafMessagePublicID: item.PublicID})
}
