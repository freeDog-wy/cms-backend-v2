package hdl

import (
	"github.com/gin-gonic/gin"
)

type Hdl interface {
	RegisterRoutes(route *gin.Engine)
}
