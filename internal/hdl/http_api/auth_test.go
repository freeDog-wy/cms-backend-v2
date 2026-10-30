package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoginBodyLimit(t *testing.T) {
	largeJSON := `{"username":"` + strings.Repeat("a", 5*1024) + `","password":"password"}`
	for _, tc := range []struct {
		name          string
		body          string
		unknownLength bool
		wantStatus    int
		wantCode      int
	}{
		{"large declared length", largeJSON, false, 413, 1006},
		{"large unknown length", largeJSON, true, 413, 1006},
		{"large trailing data", `{"username":"admin","password":"password"}` + strings.Repeat(" ", 5*1024), true, 413, 1006},
		{"invalid credentials fields", `{}`, false, 400, 1000},
		{"malformed JSON", `{"username":`, false, 400, 1000},
		{"exact limit invalid fields", `{}` + strings.Repeat(" ", 4*1024-2), true, 400, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			// Rejected requests must not call the authentication service.
			h := NewAuthHdl(nil, nil)
			h.RegisterRoutes(router)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.unknownLength {
				req.ContentLength = -1
				req.TransferEncoding = []string{"chunked"}
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			var response struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.wantCode {
				t.Fatalf("code=%d, want %d", response.Code, tc.wantCode)
			}
			if recorder.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected request set a session cookie")
			}
		})
	}
}
