package httpapi

import (
	"net/http"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	httpmiddleware "github.com/freeDog-wy/cms-backend-v2/internal/hdl/http_middleware"
	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
	"github.com/gin-gonic/gin"
)

type AuthHdl struct {
	authSrv *srv.AuthSrv
}

func NewAuthHdl(authSrv *srv.AuthSrv) *AuthHdl {
	return &AuthHdl{
		authSrv: authSrv,
	}
}

func (h *AuthHdl) RegisterRoutes(route *gin.Engine) {
	g := route.Group("/api/v1")

	// g.POST("/register", h.register)

	// 同一个 IP，60 秒内允许提交 20 次登录请求，成功和失败都计数。最多只能有 10 个不同IP尝试登录
	loginLimiter := httpmiddleware.IPRateLimit(20, time.Minute, 10)

	g.POST("/login", loginLimiter, httpmiddleware.BodyLimit(4*1024), h.login)
}

type LoginRequest struct {
	Username string `json:"username" binding:"required,alphanum,max=64"`
	Password string `json:"password" binding:"required,max=128"`
}

type LoginResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *AuthHdl) login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		hdl.EchoError(c, errorx.ErrInvalidArgument)
		return
	}
	result, err := h.authSrv.Login(c.Request.Context(), srv.UserLoginCmd{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     "session_token",
		Value:    result.Token,
		Path:     "/",
		Expires:  result.ExpiresAt,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	hdl.EchoOK(c, "login success", nil)
}

// type RegisterRequest struct {
// 	Username string `json:"username" binding:"required,alphanum,min=3,max=64"`
// 	Nickname string `json:"nickname" binding:"required,alphanum,min=3,max=64"`
// 	Email    string `json:"email"`
// 	Password string `json:"password" binding:"required,min=15,max=128"`
// }

// type RegisterResponse struct {
// 	ID       int64  `json:"id"`
// 	Username string `json:"username"`
// }

// func (h *AuthHdl) register(c *gin.Context) {
// 	var req RegisterRequest
// 	if err := c.ShouldBindJSON(&req); err != nil {
// 		hdl.EchoError(c, errorx.ErrInvalidArgument)
// 		return
// 	}
// 	user, err := h.authSrv.Register(c.Request.Context(), srv.UserRegisterCmd{
// 		Username: req.Username,
// 		Password: req.Password,
// 	})
// 	if err != nil {
// 		hdl.EchoError(c, err)
// 		return
// 	}
// 	hdl.Echo(c, http.StatusCreated, 0, "register success", &RegisterResponse{
// 		ID:       user.ID,
// 		Username: user.Username,
// 	})
// }
