package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRequireSessionRejectsMissingOrMalformedCookies(t *testing.T) {
	for _, token := range []string{"", "invalid-token", strings.Repeat("!", 43)} {
		t.Run(token, func(t *testing.T) {
			// 无效凭据应在访问数据库前拒绝。
			auth := srv.NewAuthSrv(nil, nil)
			engine := gin.New()
			engine.GET("/", RequireSession(auth), func(c *gin.Context) {
				t.Fatal("unauthenticated request reached handler")
			})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if token != "" {
				req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// 使用已关闭的连接池验证真实 Service 的数据库错误传播，不建立网络连接。
func closedAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 port=1 user=test dbname=test sslmode=disable"), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAuthenticationDatabaseFailureIsInternalError(t *testing.T) {
	auth := srv.NewAuthSrv(closedAuthTestDB(t), nil)
	engine := gin.New()
	engine.GET("/", RequireSession(auth), func(c *gin.Context) { t.Fatal("failed authentication reached handler") })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: strings.Repeat("a", 43)})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPermissionRequiresAuthentication(t *testing.T) {
	engine := gin.New()
	engine.GET("/", RequirePermission(srv.NewRBACSrv(nil), "comment:moderate"), func(c *gin.Context) { t.Fatal("missing identity reached handler") })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestPermissionDatabaseFailureIsInternalError(t *testing.T) {
	rbac := srv.NewRBACSrv(closedAuthTestDB(t))
	engine := gin.New()
	engine.GET("/", func(c *gin.Context) { c.Set(currentUserKey, int64(42)) }, RequirePermission(rbac, "comment:moderate"), func(c *gin.Context) { t.Fatal("failed permission query reached handler") })
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
