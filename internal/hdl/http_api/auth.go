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
	rbacSrv *srv.RBACSrv
}

func NewAuthHdl(authSrv *srv.AuthSrv, rbacSrv *srv.RBACSrv) *AuthHdl {
	return &AuthHdl{
		authSrv: authSrv,
		rbacSrv: rbacSrv,
	}
}

func (h *AuthHdl) RegisterRoutes(route *gin.Engine) {
	g := route.Group("/api/v1/auth")

	// g.POST("/register", h.register)

	// 同一个 IP，60 秒内允许提交 20 次登录请求，成功和失败都计数。最多只能有 10 个不同IP尝试登录
	loginLimiter := httpmiddleware.IPRateLimit(20, time.Minute, 10)

	g.POST("/login", loginLimiter, httpmiddleware.BodyLimit(4*1024), h.login)
	g.GET("/me", httpmiddleware.RequireSession(h.authSrv), h.me)
	g.POST("/logout", h.logout)
}

func (h *AuthHdl) me(c *gin.Context) {
	id, ok := httpmiddleware.CurrentUserID(c)
	if !ok {
		hdl.EchoError(c, errorx.ErrUnauthenticated)
		return
	}
	permissions, err := h.rbacSrv.ListPermissions(c.Request.Context(), id)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	if permissions == nil {
		permissions = []string{}
	}
	hdl.EchoOK(c, "success", struct {
		ID          int64    `json:"id,string"`
		Permissions []string `json:"permissions"`
	}{id, permissions})
}

func (h *AuthHdl) logout(c *gin.Context) {
	token, _ := c.Cookie("session_token")
	if err := h.authSrv.Logout(c.Request.Context(), token); err != nil {
		hdl.EchoError(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "session_token", Path: "/", Value: "", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	hdl.EchoOK(c, "success", nil)
}

type LoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
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
