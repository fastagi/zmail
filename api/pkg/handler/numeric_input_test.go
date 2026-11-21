package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type numericQueryProbe struct {
	PerPage int  `form:"per_page"`
	OwnerID uint `form:"owner_id"`
}

type numericBodyProbe struct {
	OwnerID uint `json:"owner_id"`
}

func numericInputEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/items/:id", func(c *gin.Context) {
		if _, ok := ParseID(c, "id"); ok {
			c.Status(http.StatusNoContent)
		}
	})
	engine.GET("/signed/:id", func(c *gin.Context) {
		if _, ok := ParseInt64ID(c, "id"); ok {
			c.Status(http.StatusNoContent)
		}
	})
	engine.GET("/query", func(c *gin.Context) {
		var query numericQueryProbe
		if BindQuery(c, &query) {
			c.Status(http.StatusNoContent)
		}
	})
	engine.GET("/validated-query", func(c *gin.Context) {
		var query numericQueryProbe
		if err := c.ShouldBindQuery(&query); err != nil {
			WriteBodyError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
	engine.GET("/uri/:id", func(c *gin.Context) {
		var uri struct {
			ID uint `uri:"id"`
		}
		if BindURI(c, &uri) {
			c.Status(http.StatusNoContent)
		}
	})
	engine.POST("/body", func(c *gin.Context) {
		var body numericBodyProbe
		if BindJSON(c, &body) {
			c.Status(http.StatusNoContent)
		}
	})
	return engine
}

// Malformed or out-of-range numbers answer 400 COMMON.INVALID_INPUT with a stable detail that
// never exposes Go parser or type names.
func TestNumericInputFailuresAreStableAndImplementationFree(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantDetail string
	}{
		{name: "path id out of range", method: http.MethodGet, path: "/items/99999999999999999999", wantDetail: "id is invalid"},
		{name: "path id malformed", method: http.MethodGet, path: "/items/abc", wantDetail: "id is invalid"},
		{name: "path id negative", method: http.MethodGet, path: "/items/-1", wantDetail: "id is invalid"},
		{name: "path id zero", method: http.MethodGet, path: "/items/0", wantDetail: "id is invalid"},
		{name: "signed path id out of range", method: http.MethodGet, path: "/signed/99999999999999999999", wantDetail: "id is invalid"},
		{name: "query page size out of range", method: http.MethodGet, path: "/query?per_page=99999999999999999999", wantDetail: "query parameter is not a valid number"},
		{name: "query id malformed", method: http.MethodGet, path: "/query?owner_id=x", wantDetail: "query parameter is not a valid number"},
		{name: "validated query out of range", method: http.MethodGet, path: "/validated-query?owner_id=99999999999999999999", wantDetail: "query parameter is not a valid number"},
		{name: "uri id out of range", method: http.MethodGet, path: "/uri/99999999999999999999", wantDetail: "path parameter is not a valid number"},
		{name: "body id out of range", method: http.MethodPost, path: "/body", body: `{"owner_id":99999999999999999999}`, wantDetail: "owner_id has an invalid value"},
		{name: "body id wrong type", method: http.MethodPost, path: "/body", body: `{"owner_id":"7"}`, wantDetail: "owner_id has an invalid value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := send(numericInputEngine(), tt.method, tt.path, tt.body)
			require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
			var payload struct {
				ErrorCode string `json:"error_code"`
				Error     string `json:"error"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
			assert.Equal(t, "COMMON.INVALID_INPUT", payload.ErrorCode)
			assert.Equal(t, tt.wantDetail, payload.Error)
			for _, leaked := range []string{"strconv", "Parse", "out of range", "Go struct", "uint", "json:"} {
				assert.NotContains(t, recorder.Body.String(), leaked)
			}
		})
	}
}
