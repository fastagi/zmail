package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CursorMeta describes one keyset page. Unlike offset pagination it carries no total, so a page
// costs the same however many records match.
type CursorMeta struct {
	PerPage    int     `json:"per_page"`
	HasMore    bool    `json:"has_more"`
	NextCursor *string `json:"next_cursor"`
}

// CursorPageResponse is the envelope for a keyset page.
type CursorPageResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    any        `json:"data"`
	Meta    CursorMeta `json:"meta"`
}

// SuccessCursorPage writes one keyset page. An empty nextCursor means there is no further page.
func SuccessCursorPage(c *gin.Context, items any, perPage int, nextCursor string) {
	meta := CursorMeta{PerPage: perPage}
	if nextCursor != "" {
		meta.HasMore = true
		meta.NextCursor = &nextCursor
	}
	c.JSON(http.StatusOK, CursorPageResponse{Code: 0, Message: "success", Data: items, Meta: meta})
}

// MetaResponse is the envelope for a list whose meta has a resource-specific shape.
type MetaResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	Meta    any    `json:"meta"`
}

// SuccessWithMeta writes a list with its own meta object, for pages whose meta is neither the
// offset nor the generic keyset shape.
func SuccessWithMeta(c *gin.Context, items, meta any) {
	c.JSON(http.StatusOK, MetaResponse{Code: 0, Message: "success", Data: items, Meta: meta})
}

// SuccessCursorPageWithMeta writes one keyset page whose meta adds resource-specific keys to the
// generic paging keys. per_page, has_more, and next_cursor always describe the page itself, even
// when extra names them too.
func SuccessCursorPageWithMeta(c *gin.Context, items any, perPage int, nextCursor string, extra map[string]any) {
	meta := make(map[string]any, len(extra)+3)
	for key, value := range extra {
		meta[key] = value
	}
	meta["per_page"] = perPage
	meta["has_more"] = nextCursor != ""
	if nextCursor != "" {
		meta["next_cursor"] = nextCursor
	} else {
		meta["next_cursor"] = nil
	}
	c.JSON(http.StatusOK, MetaResponse{Code: 0, Message: "success", Data: items, Meta: meta})
}
