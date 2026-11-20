package response

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSuccessCursorPageWithMetaKeepsPagingKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	SuccessCursorPageWithMeta(c, []int{1}, 15, "next", map[string]any{"per_page": 1, "has_more": false, "sent_last_24h": 37})

	var body struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Meta["per_page"] != float64(15) || body.Meta["has_more"] != true || body.Meta["next_cursor"] != "next" {
		t.Fatalf("paging keys = %v; the page's own values must win", body.Meta)
	}
	if body.Meta["sent_last_24h"] != float64(37) {
		t.Fatalf("extra keys = %v", body.Meta)
	}

	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	SuccessCursorPageWithMeta(c, []int{}, 15, "", nil)
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if next, present := body.Meta["next_cursor"]; !present || next != nil || body.Meta["has_more"] != false {
		t.Fatalf("last page meta = %v; want next_cursor null and has_more false", body.Meta)
	}
}
