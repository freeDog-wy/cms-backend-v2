package hdl

import (
	"errors"
	"net/http"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/gin-gonic/gin"
)

func Echo(c *gin.Context, status_code int, code int, msg string, data any) error {
	c.JSON(status_code, gin.H{
		"code":    code,
		"message": msg,
		"data":    data,
	})
	return nil
}

func EchoOK(c *gin.Context, msg string, data any) error {
	return Echo(c, http.StatusOK, 0, msg, data)
}

func EchoError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, 5000, "服务器内部错误"

	switch {
	case errors.Is(err, errorx.ErrRequestBodyTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, 1006, "请求体过大"

	case errors.Is(err, errorx.ErrServiceBusy):
		status, code, message = http.StatusServiceUnavailable, 5001, "服务繁忙，请稍后重试"

	case errors.Is(err, errorx.ErrInvalidArgument):
		status, code, message = http.StatusBadRequest, 1000, "请求参数不合法"

	case errors.Is(err, errorx.ErrInvalidCredentials),
		errors.Is(err, errorx.ErrUserDisabled):
		status, code, message = http.StatusUnauthorized, 1001, "用户名或密码错误，或账号不可用"

	case errors.Is(err, errorx.ErrUsernameExists):
		status, code, message = http.StatusConflict, 1002, "用户名已存在"

	case errors.Is(err, errorx.ErrEmailExists):
		status, code, message = http.StatusConflict, 1003, "邮箱已存在"

	case errors.Is(err, errorx.ErrRequestOriginFobidden):
		status, code, message = http.StatusForbidden, 1004, "请求来源不受信任"

	case errors.Is(err, errorx.ErrTooManyRequest):
		status, code, message = http.StatusTooManyRequests, 1005, "请求过于频繁，请稍后重试"
	case errors.Is(err, errorx.ErrUnauthenticated):
		status, code, message = http.StatusUnauthorized, 1007, "请先登录"
	case errors.Is(err, errorx.ErrForbidden):
		status, code, message = http.StatusForbidden, 1008, "没有操作权限"
	case errors.Is(err, errorx.ErrNotFound):
		status, code, message = http.StatusNotFound, 1009, "资源不存在"
	case errors.Is(err, errorx.ErrConflict):
		status, code, message = http.StatusConflict, 1010, "数据已变化或提交标识冲突，请刷新后重试"
	case errors.Is(err, errorx.ErrCommentsClosed):
		status, code, message = http.StatusForbidden, 1011, "文章已关闭评论"
	case errors.Is(err, errorx.ErrCommentUnavailable):
		status, code, message = http.StatusServiceUnavailable, 5002, "评论服务暂不可用"
	default:
		// 将内部错误交给 Gin 的错误记录机制。
		// 配合日志中间件记录，响应中不暴露底层错误。
		c.Error(err)
	}

	c.Abort()
	Echo(c, status, code, message, nil)
}
