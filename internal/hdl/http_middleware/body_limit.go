package httpmiddleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	"github.com/gin-gonic/gin"
)

// BodyLimit 在执行后续处理函数前，将请求体读入内存并检查其大小，最多允许 maxBytes 字节。
// 适用于请求体较小的接口；流式上传应采用边读取边限制大小的方式。
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		panic("request body limit must be positive")
	}

	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			hdl.EchoError(c, errorx.ErrRequestBodyTooLarge)
			return
		}

		limitedBody := http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		defer limitedBody.Close()

		// 检查完整请求体，包括分块传输的数据和 JSON 后附带的数据。
		body, err := io.ReadAll(limitedBody)
		if err != nil {
			var sizeErr *http.MaxBytesError
			if errors.As(err, &sizeErr) {
				hdl.EchoError(c, errorx.ErrRequestBodyTooLarge)
			} else {
				hdl.EchoError(c, errorx.ErrInvalidArgument)
			}
			return
		}

		// 重建请求体，供后续参数绑定读取。
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	}
}
