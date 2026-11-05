package upload

import (
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/attachmentdelivery"
	"github.com/gin-gonic/gin"
)

type Module struct{ Handler *Handler }

func NewModule(handler *Handler) *Module { return &Module{Handler: handler} }
func (m *Module) RegisterPublicRoutes(api *gin.RouterGroup) {
	api.GET(attachmentdelivery.RoutePath, m.Handler.Fetch)
	api.HEAD(attachmentdelivery.RoutePath, m.Handler.Fetch)
}
