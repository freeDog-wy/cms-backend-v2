package httpmiddleware

import (
	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
	"github.com/gin-gonic/gin"
)

const currentUserKey = "authenticated_user_id"

// CurrentUserID 只读取认证中间件写入的用户身份。
func CurrentUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get(currentUserKey)
	id, valid := v.(int64)
	return id, ok && valid && id > 0
}

func RequireSession(auth *srv.AuthSrv) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("session_token")
		if err != nil {
			hdl.EchoError(c, errorx.ErrUnauthenticated)
			return
		}
		id, err := auth.Authenticate(c.Request.Context(), token)
		if err != nil {
			hdl.EchoError(c, err)
			return
		}
		if id <= 0 {
			hdl.EchoError(c, errorx.ErrUnauthenticated)
			return
		}
		c.Set(currentUserKey, id)
		c.Next()
	}
}

func RequirePermission(rbac *srv.RBACSrv, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := CurrentUserID(c)
		if !ok {
			hdl.EchoError(c, errorx.ErrUnauthenticated)
			return
		}
		allowed, err := rbac.HasPermission(c.Request.Context(), id, permission)
		if err != nil {
			hdl.EchoError(c, err)
			return
		}
		if !allowed {
			hdl.EchoError(c, errorx.ErrForbidden)
			return
		}
		c.Next()
	}
}
