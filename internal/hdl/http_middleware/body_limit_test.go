package httpmiddleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBodyLimitPreservesBodyAndStopsOversizedRequests(t *testing.T) {
	for _, tc := range []struct {
		name          string
		body          string
		unknownLength bool
		allowed       bool
	}{
		{"small", "abc", false, true},
		{"exact limit", "12345678", true, true},
		{"over limit", "123456789", false, false},
		{"over limit unknown length", "123456789", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			router := gin.New()
			router.POST("/", BodyLimit(8), func(c *gin.Context) {
				called = true
				body, err := io.ReadAll(c.Request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != tc.body {
					t.Fatalf("downstream body=%q, want %q", body, tc.body)
				}
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			if tc.unknownLength {
				req.ContentLength = -1
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			wantStatus := http.StatusRequestEntityTooLarge
			if tc.allowed {
				wantStatus = http.StatusNoContent
			}
			if recorder.Code != wantStatus || called != tc.allowed {
				t.Fatalf("status=%d called=%v, want status=%d called=%v", recorder.Code, called, wantStatus, tc.allowed)
			}
		})
	}
}
