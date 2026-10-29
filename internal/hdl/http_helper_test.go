package hdl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/gin-gonic/gin"
)

func TestEchoErrorServiceBusy(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	EchoError(ctx, fmt.Errorf("password operation: %w", errorx.ErrServiceBusy))
	if recorder.Code != http.StatusServiceUnavailable || !ctx.IsAborted() {
		t.Fatalf("status=%d aborted=%v", recorder.Code, ctx.IsAborted())
	}
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 5001 {
		t.Fatalf("business code=%d", body.Code)
	}
}
