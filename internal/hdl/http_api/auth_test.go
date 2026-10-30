package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

func TestLoginRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{"admin with underscore", "freeDog_wy", "password", false},
		{"admin with hyphen", "freeDog-wy", "password", false},
		{"unicode username", "管理员", "password", false},
		{"minimum username", "abc", "password", false},
		{"maximum username", strings.Repeat("a", 64), "password", false},
		{"empty username", "", "password", true},
		{"short username", "ab", "password", true},
		{"long username", strings.Repeat("a", 65), "password", true},
		{"empty password", "admin", "", true},
		{"long password", "admin", strings.Repeat("a", 129), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(LoginRequest{Username: tc.username, Password: tc.password})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			var payload LoginRequest
			if err := binding.JSON.Bind(req, &payload); (err != nil) != tc.wantErr {
				t.Fatalf("binding error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

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
