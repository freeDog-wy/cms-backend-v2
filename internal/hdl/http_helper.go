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
	status := http.StatusInternalServerError
	code := 5000
	message := "服务器内部错误"

	switch {
	case errors.Is(err, errorx.ErrRequestBodyTooLarge):
		status = http.StatusRequestEntityTooLarge
		code = 1006
		message = "请求体过大"

	case errors.Is(err, errorx.ErrServiceBusy):
		status = http.StatusServiceUnavailable
		code = 5001
		message = "服务繁忙，请稍后重试"

	case errors.Is(err, errorx.ErrInvalidArgument):
		status = http.StatusBadRequest
		code = 1000
		message = "请求参数不合法"

	case errors.Is(err, errorx.ErrInvalidCredentials),
		errors.Is(err, errorx.ErrUserDisabled):
		status = http.StatusUnauthorized
		code = 1001
		message = "用户名或密码错误，或账号不可用"

	case errors.Is(err, errorx.ErrUsernameExists):
		status = http.StatusConflict
		code = 1002
		message = "用户名已存在"

	case errors.Is(err, errorx.ErrEmailExists):
		status = http.StatusConflict
		code = 1003
		message = "邮箱已存在"

	case errors.Is(err, errorx.ErrRequestOriginFobidden):
		status = http.StatusForbidden
		code = 1004
		message = "请求来源不受信任"

	case errors.Is(err, errorx.ErrTooManyRequest):
		status = http.StatusTooManyRequests
		code = 1005
		message = "请求过于频繁，请稍后重试"

	default:
		// 将内部错误交给 Gin 的错误记录机制。
		// 配合日志中间件记录，响应中不暴露底层错误。
		c.Error(err)
	}

	c.Abort()
	Echo(c, status, code, message, nil)
}
