package httpmiddleware

import (
	"strconv"
	"sync"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/hdl"
	"github.com/gin-gonic/gin"
)

type ipWindow struct {
	count   int
	resetAt time.Time
}

func IPRateLimit(
	maxRequests int,
	window time.Duration,
	maxIPs int,
) gin.HandlerFunc {
	if maxRequests <= 0 || window <= 0 || maxIPs <= 0 {
		panic("invalid IP rate limit configuration")
	}

	var mu sync.Mutex
	buckets := make(map[string]ipWindow)
	nextCleanup := time.Now().Add(window)

	allow := func(ip string) (bool, time.Duration) {
		mu.Lock()
		defer mu.Unlock()

		now := time.Now()

		// 按需清理过期记录，不额外启动后台 goroutine。
		if !now.Before(nextCleanup) {
			for key, bucket := range buckets {
				if !now.Before(bucket.resetAt) {
					delete(buckets, key)
				}
			}
			nextCleanup = now.Add(window)
		}

		bucket, exists := buckets[ip]
		if exists && !now.Before(bucket.resetAt) {
			delete(buckets, ip)
			exists = false
		}

		if !exists {
			// 限制记录数量，避免大量不同 IP 造成内存增长。
			if len(buckets) >= maxIPs {
				return false, nextCleanup.Sub(now)
			}

			bucket = ipWindow{
				resetAt: now.Add(window),
			}
		}

		if bucket.count >= maxRequests {
			return false, bucket.resetAt.Sub(now)
		}

		bucket.count++
		buckets[ip] = bucket
		return true, 0
	}

	return func(c *gin.Context) {
		allowed, retryAfter := allow(c.ClientIP())
		if !allowed {
			// 向上取整为秒。
			seconds := (retryAfter + time.Second - 1) / time.Second
			c.Header(
				"Retry-After",
				strconv.FormatInt(int64(seconds), 10),
			)

			hdl.EchoError(c, errorx.ErrTooManyRequest)
			return
		}

		// 执行业务前已经释放锁。
		c.Next()
	}
}
