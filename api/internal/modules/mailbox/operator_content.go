package mailbox

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/html"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/pagination"
	"github.com/fastagi/zmail/api/pkg/response"
)

// operatorContentReasonMax bounds the reason an operator gives for reading a body, in characters.
const operatorContentReasonMax = 500

// Content text sources: the stored text part, or the HTML part converted to readable text.
const (
	contentTextSourceText = "text"
	contentTextSourceHTML = "html"
)

// OperatorBroadcastContent is the sent text of one organization broadcast message. It carries no
// HTML, image, attachment, or recipient.
type OperatorBroadcastContent struct {
	AccessID       uint      `json:"access_id"`
	OrganizationID uint      `json:"organization_id"`
	MessageID      uint      `json:"message_id"`
	BroadcastID    uint      `json:"broadcast_id"`
	Subject        string    `json:"subject"`
	FromAddress    string    `json:"from_address"`
	CreatedAt      time.Time `json:"created_at"`
	Text           string    `json:"text"`
	TextSource     string    `json:"text_source"`
}

// OperatorContentAccess is one record of an operator reading a broadcast body.
type OperatorContentAccess struct {
	ID               uint      `json:"id"`
	ViewerUserID     uint      `json:"viewer_user_id"`
	OrganizationID   uint      `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	MessageID        uint      `json:"message_id"`
	Reason           string    `json:"reason"`
	AccessedAt       time.Time `json:"accessed_at"`
}

type operatorContentRequest struct {
	Reason *string `json:"reason"`
}

// ReadBroadcastContent serves POST /v1/operator/outbound-messages/:organization_id/:message_id/content.
// It returns a broadcast's sent text only after the access record is stored. Neither the body nor
// the reason is ever logged.
func (h *OperatorMetricsHandler) ReadBroadcastContent(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	organizationID, organizationOK := optionalPositiveID(c.Param("organization_id"))
	messageID, messageOK := optionalPositiveID(c.Param("message_id"))
	if !organizationOK || !messageOK || organizationID == 0 || messageID == 0 {
		invalidMetricsInput(c)
		return
	}
	var request operatorContentRequest
	if !handler.BindJSON(c, &request) {
		return
	}
	reason, ok := contentReason(request.Reason)
	if !ok {
		response.ValidationFailed(c, map[string][]string{
			"reason": {"The reason is required, at most 500 characters, without control characters"},
		})
		return
	}
	viewerID, ok := handler.GetUserID(c)
	if !ok {
		return
	}
	row, err := h.store.readBroadcastContent(c.Request.Context(), broadcastContentRequest{
		ViewerUserID: viewerID, OrganizationID: organizationID, MessageID: messageID, Reason: reason, At: h.now(),
	})
	if err != nil {
		h.contentError(c, err)
		return
	}
	text, source := row.TextBody, contentTextSourceText
	if strings.TrimSpace(text) == "" && strings.TrimSpace(row.HTMLBody) != "" {
		text, source = readableHTMLText(row.HTMLBody), contentTextSourceHTML
	}
	response.Success(c, OperatorBroadcastContent{
		AccessID: row.AccessID, OrganizationID: organizationID, MessageID: messageID, BroadcastID: row.BroadcastID,
		Subject: row.Subject, FromAddress: row.FromAddress, CreatedAt: row.CreatedAt.UTC(), Text: text, TextSource: source,
	})
}

// ContentAccessAudits serves GET /v1/operator/content-access-audits, newest first.
func (h *OperatorMetricsHandler) ContentAccessAudits(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	var filter contentAuditFilter
	var ok bool
	if filter.OrganizationID, ok = optionalPositiveID(c.Query("organization_id")); !ok {
		invalidMetricsInput(c)
		return
	}
	if filter.ViewerUserID, ok = optionalPositiveID(c.Query("viewer_user_id")); !ok {
		invalidMetricsInput(c)
		return
	}
	if filter.From, ok = optionalInstant(c.Query("from")); !ok {
		invalidMetricsInput(c)
		return
	}
	if filter.To, ok = optionalInstant(c.Query("to")); !ok {
		invalidMetricsInput(c)
		return
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		invalidMetricsInput(c)
		return
	}
	page, perPage, pageOK := bindMetricsPage(c)
	if !pageOK {
		invalidMetricsInput(c)
		return
	}
	rows, total, err := h.store.contentAccessAudits(c.Request.Context(), filter, page, perPage)
	if err != nil {
		h.contentError(c, err)
		return
	}
	ids := make([]uint, 0, len(rows))
	seen := make(map[uint]bool, len(rows))
	for _, row := range rows {
		if !seen[row.OrganizationID] {
			seen[row.OrganizationID] = true
			ids = append(ids, row.OrganizationID)
		}
	}
	names, err := h.organizationsDirectory.PlatformOrganizationNames(c.Request.Context(), ids)
	if err != nil {
		h.contentError(c, err)
		return
	}
	records := make([]OperatorContentAccess, len(rows))
	for index, row := range rows {
		records[index] = OperatorContentAccess{
			ID: row.ID, ViewerUserID: row.ViewerUserID, OrganizationID: row.OrganizationID,
			OrganizationName: names[row.OrganizationID], MessageID: row.MessageID, Reason: row.Reason,
			AccessedAt: row.AccessedAt.UTC(),
		}
	}
	paginator := pagination.NewPaginator(records, total, page, perPage)
	paginator.SetPath(c.Request.URL.Path).WithQuery(c.Request.URL.Query())
	response.Success(c, paginator)
}

// contentError maps a refused read to its code and every other failure to a retryable 503. The
// logged error never carries a body.
func (h *OperatorMetricsHandler) contentError(c *gin.Context, err error) {
	if errors.Is(err, ErrMailMessageNotFound) || errors.Is(err, domain.ErrOperatorContentNotEligible) {
		mapper := h.errors
		if mapper == nil {
			mapper = response.DefaultErrorMapper
		}
		response.HandleErrorWithMapper(c, "Operator content read refused", err, mapper)
		return
	}
	slog.ErrorContext(c.Request.Context(), "mailbox.operator_content_failed", "err", err)
	response.AbortWithCode(c, http.StatusServiceUnavailable, response.ErrorCodeServiceUnavailable, "Mail content service unavailable")
}

// contentReason trims the reason and accepts 1 to 500 characters with no control character other
// than line breaks and tabs.
func contentReason(raw *string) (string, bool) {
	if raw == nil {
		return "", false
	}
	reason := strings.TrimSpace(*raw)
	if reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > operatorContentReasonMax {
		return "", false
	}
	if strings.IndexFunc(reason, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) >= 0 {
		return "", false
	}
	return reason, true
}

func optionalInstant(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

// readableHTMLText turns an HTML body into plain text an operator can read: script, style, head,
// and similar content is dropped, block elements and <br> end a line, entities are decoded, and
// nothing is fetched. Runs of spaces collapse and empty lines are dropped.
func readableHTMLText(body string) string {
	if body == "" {
		return ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(body))
	var out strings.Builder
	skip := 0
	for {
		token := tokenizer.Next()
		switch token {
		case html.ErrorToken:
			return tidyReadableText(out.String())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			tag := string(name)
			if hiddenHTMLElement(tag) {
				if token == html.StartTagToken {
					skip++
				} else if token == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			switch {
			case tag == "br":
				out.WriteByte('\n')
			case blockHTMLElement(tag):
				out.WriteByte('\n')
			default:
				out.WriteByte(' ')
			}
		case html.TextToken:
			if skip == 0 {
				out.Write(tokenizer.Text())
			}
		case html.CommentToken, html.DoctypeToken:
		}
	}
}

func hiddenHTMLElement(tag string) bool {
	switch tag {
	case "script", "style", "head", "title", "noscript", "template", "svg", "iframe", "object":
		return true
	}
	return false
}

func blockHTMLElement(tag string) bool {
	switch tag {
	case "p", "div", "li", "ul", "ol", "tr", "table", "tbody", "thead", "section", "article", "header",
		"footer", "blockquote", "pre", "hr", "h1", "h2", "h3", "h4", "h5", "h6", "body", "html":
		return true
	}
	return false
}

// tidyReadableText collapses spaces within each line and drops empty lines.
func tidyReadableText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
