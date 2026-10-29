package httpmiddleware

import (
	"net/http"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	"github.com/gin-gonic/gin"
)

func Csrf(csrf *http.CrossOriginProtection) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := csrf.Check(c.Request); err != nil {
			hdl.EchoError(c, errorx.ErrRequestOriginFobidden)
			return
		}
		c.Next()
	}
}