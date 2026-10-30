package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	httpmiddleware "github.com/freeDog-wy/cms-backend-v2/internal/hdl/http_middleware"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
	"github.com/gin-gonic/gin"
)

type CommentHdl struct {
	commentSrv *srv.CommentSrv
	autSrv     *srv.AuthSrv
	rbacSrv    *srv.RBACSrv
}

func NewCommentHdl(commentSrv *srv.CommentSrv, authSrv *srv.AuthSrv, rbacSrv *srv.RBACSrv) *CommentHdl {
	return &CommentHdl{
		commentSrv: commentSrv,
		autSrv:     authSrv,
		rbacSrv:    rbacSrv,
	}
}

func (h *CommentHdl) RegisterRoutes(engine *gin.Engine) {
	v1 := engine.Group("/api/v1")
	v1.GET("/articles/:key/comments", h.listRoots)
	v1.GET("/comments/:id/replies", h.listReplies)

	// 所有文章共用一个限流器；额度按客户端 IP 累计。
	v1.POST("/articles/:key/comments",
		httpmiddleware.IPRateLimit(5, time.Minute, 10000),
		httpmiddleware.BodyLimit(16*1024),
		h.createGuest,
	)

	admin := v1.Group("/admin",
		httpmiddleware.RequireSession(h.autSrv),
		httpmiddleware.RequirePermission(h.rbacSrv, srv.PermissionCommentModerate),
	)
	admin.GET("/comments", h.listAdmin)
	admin.PATCH("/comments/:id/moderation",
		httpmiddleware.BodyLimit(4*1024),
		h.moderate,
	)
	admin.DELETE("/comments/:id",
		httpmiddleware.BodyLimit(4*1024),
		h.remove,
	)
	admin.POST("/articles/:key/comments",
		httpmiddleware.BodyLimit(16*1024),
		h.createStaff,
	)
	admin.GET("/comment-manifest", func(c *gin.Context) {
		hdl.EchoOK(c, "success", h.commentSrv.ManifestStatus())
	})
	admin.POST("/comment-manifest/refresh", func(c *gin.Context) {
		if err := h.commentSrv.RefreshManifest(c.Request.Context()); err != nil {
			c.Error(err)
			hdl.EchoError(c, errorx.ErrCommentUnavailable)
			return
		}
		hdl.EchoOK(c, "success", h.commentSrv.ManifestStatus())
	})
}

// ID 按字符串传输，避免 JavaScript 大整数精度丢失。
type guestCommentRequest struct {
	GuestName string `json:"guest_name"`
	Content   string `json:"content"`
	ParentID  *int64 `json:"parent_id,string"`
	RequestID string `json:"request_id"`
}

func (h *CommentHdl) createGuest(c *gin.Context) {
	var req guestCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		hdl.EchoError(c, errorx.ErrInvalidArgument)
		return
	}
	result, err := h.commentSrv.CreateGuest(c.Request.Context(), srv.CreateGuestCommentCmd{ArticleKey: c.Param("key"), ParentID: req.ParentID, GuestName: req.GuestName, Content: req.Content, RequestID: req.RequestID})
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.Echo(c, http.StatusAccepted, 0, "评论已提交，等待审核", result)
}

func commentPaging(c *gin.Context, cursorName string) (int64, int, error) {
	var cursor int64
	var limit int
	var err error
	if v, exists := c.GetQuery(cursorName); exists {
		cursor, err = strconv.ParseInt(v, 10, 64)
		if err != nil || cursor < 0 {
			return 0, 0, errorx.ErrInvalidArgument
		}
	}
	if v, exists := c.GetQuery("limit"); exists {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, errorx.ErrInvalidArgument
		}
	}
	return cursor, limit, nil
}

func commentPathID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errorx.ErrInvalidArgument
	}
	return id, nil
}

func (h *CommentHdl) listRoots(c *gin.Context) {
	after, limit, err := commentPaging(c, "after_id")
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	result, err := h.commentSrv.ListPublic(c.Request.Context(), c.Param("key"), nil, after, limit)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.EchoOK(c, "success", result)
}

func (h *CommentHdl) listReplies(c *gin.Context) {
	id, err := commentPathID(c)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	after, limit, err := commentPaging(c, "after_id")
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	result, err := h.commentSrv.ListReplies(c.Request.Context(), id, after, limit)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.EchoOK(c, "success", result)
}

func (h *CommentHdl) listAdmin(c *gin.Context) {
	before, limit, err := commentPaging(c, "before_id")
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	var key *string
	if v, exists := c.GetQuery("article_key"); exists {
		key = &v
	}
	result, err := h.commentSrv.ListForAdmin(
		c.Request.Context(),
		model.CommentStatus(c.Query("status")),
		key,
		before,
		limit,
	)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.EchoOK(c, "success", result)
}

type moderateCommentRequest struct {
	Version int64               `json:"version"`
	Status  model.CommentStatus `json:"status"`
	Reason  string              `json:"reason"`
}

func (h *CommentHdl) moderate(c *gin.Context) {
	id, err := commentPathID(c)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	var req moderateCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		hdl.EchoError(c, errorx.ErrInvalidArgument)
		return
	}
	actorID, ok := httpmiddleware.CurrentUserID(c)
	if !ok {
		hdl.EchoError(c, errorx.ErrUnauthenticated)
		return
	}
	result, err := h.commentSrv.Moderate(c.Request.Context(), actorID, id, req.Version, req.Status, req.Reason)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.EchoOK(c, "success", result)
}

func (h *CommentHdl) remove(c *gin.Context) {
	id, err := commentPathID(c)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	var req struct {
		Version int64  `json:"version"`
		Reason  string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		hdl.EchoError(c, errorx.ErrInvalidArgument)
		return
	}
	actorID, ok := httpmiddleware.CurrentUserID(c)
	if !ok {
		hdl.EchoError(c, errorx.ErrUnauthenticated)
		return
	}
	if err := h.commentSrv.Delete(c.Request.Context(), actorID, id, req.Version, req.Reason); err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.EchoOK(c, "success", nil)
}

func (h *CommentHdl) createStaff(c *gin.Context) {
	var req struct {
		Content  string `json:"content"`
		ParentID *int64 `json:"parent_id,string"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		hdl.EchoError(c, errorx.ErrInvalidArgument)
		return
	}
	actorID, ok := httpmiddleware.CurrentUserID(c)
	if !ok {
		hdl.EchoError(c, errorx.ErrUnauthenticated)
		return
	}
	result, err := h.commentSrv.CreateStaff(
		c.Request.Context(),
		actorID,
		srv.CreateStaffCommentCmd{
			ArticleKey: c.Param("key"),
			ParentID:   req.ParentID,
			Content:    req.Content,
		},
	)
	if err != nil {
		hdl.EchoError(c, err)
		return
	}
	hdl.Echo(c, http.StatusCreated, 0, "success", result)
}
