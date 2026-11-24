package mailbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

const contentViewer = uint(42)

type contentFixture struct {
	*metricsFixture
	engine    *gin.Engine
	broadcast uint
	// broadcastMessage was created by a broadcast of organization 1.
	broadcastMessage uint
	// htmlOnly is a broadcast message with no text part.
	htmlOnly uint
}

// storeBroadcastMessage links a stored outbound message to a broadcast recipient row.
func storeBroadcastMessage(t *testing.T, f *metricsFixture, broadcastID uint, contactID uint, messageID uint) {
	t.Helper()
	require.NoError(t, f.repo.db.Create(&MailBroadcastRecipientPO{
		BroadcastID: broadcastID, ContactID: contactID, Email: "customer@example.com", Status: "sent", MessageID: &messageID,
	}).Error)
}

func newContentFixture(t *testing.T) *contentFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	base := newMetricsFixture(t)
	broadcast := &MailBroadcastPO{OrganizationID: 1, Name: "Launch", MailboxID: base.north.ID, TemplateID: 1, ListID: 1, Status: "sent", CreatedBy: 1}
	require.NoError(t, base.repo.db.Create(broadcast).Error)
	f := &contentFixture{metricsFixture: base, broadcast: broadcast.ID}
	f.broadcastMessage = base.sentA
	storeBroadcastMessage(t, base, broadcast.ID, 1, f.broadcastMessage)
	f.htmlOnly = storePlatformMessage(t, base.repo, base.north.ID, "outbound", "sent", "Rich", metricsDay.Add(16*time.Hour))
	require.NoError(t, base.repo.db.Model(&MailMessagePO{}).Where("id = ?", f.htmlOnly).Updates(map[string]any{
		"text_body": "",
		"html_body": `<html><head><title>Hidden title</title><style>p{color:red}</style></head><body>` +
			`<p>Hello &amp; welcome</p><img src="https://tracker.example/p.gif"><script>alert(1)</script>` +
			`<ul><li>One</li><li>Two</li></ul>Line<br>break</body></html>`,
	}).Error)
	storeBroadcastMessage(t, base, broadcast.ID, 2, f.htmlOnly)

	mapper := &response.ErrorMapper{}
	mapper.Register(domain.ErrServiceUnavailable, http.StatusServiceUnavailable, domain.CodeServiceUnavailable)
	for _, entry := range mailboxErrorCodes {
		mapper.Register(entry.err, entry.status, entry.code)
	}
	mapper.Register(domain.ErrOperatorContentNotEligible, http.StatusForbidden, domain.CodeOperatorContentNotEligible)
	base.handler.errors = mapper
	f.engine = gin.New()
	f.engine.Use(func(c *gin.Context) { c.Set("userID", contentViewer); c.Next() })
	f.engine.POST("/outbound-messages/:organization_id/:message_id/content", base.handler.ReadBroadcastContent)
	f.engine.GET("/content-access-audits", base.handler.ContentAccessAudits)
	return f
}

func (f *contentFixture) read(t *testing.T, organizationID, messageID uint, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	path := "/outbound-messages/" + uintString(organizationID) + "/" + uintString(messageID) + "/content"
	f.engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded), recorder.Body.String())
	return recorder, decoded
}

func (f *contentFixture) audits(t *testing.T) []MailContentAccessAuditPO {
	t.Helper()
	var rows []MailContentAccessAuditPO
	require.NoError(t, f.repo.db.Order("id").Find(&rows).Error)
	return rows
}

func uintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}

// Spec acceptance row 4 (data part): an allowed read returns the sent text and leaves exactly one
// audit record that holds no body, subject, or address.
func TestOperatorContentReadWritesTheAuditFirst(t *testing.T) {
	f := newContentFixture(t)
	recorder, body := f.read(t, 1, f.broadcastMessage, `{"reason":"  Complaint ticket 42  "}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	data := body["data"].(map[string]any)
	assert.Equal(t, "secret body Launch", data["text"])
	assert.Equal(t, "text", data["text_source"])
	assert.Equal(t, "Launch", data["subject"])
	assert.EqualValues(t, f.broadcast, data["broadcast_id"])
	assert.EqualValues(t, 1, data["organization_id"])
	assert.EqualValues(t, f.broadcastMessage, data["message_id"])
	for _, field := range []string{"html", "html_body", "to_addresses", "recipients", "attachments"} {
		assert.NotContains(t, data, field)
	}

	rows := f.audits(t)
	require.Len(t, rows, 1)
	assert.EqualValues(t, data["access_id"], rows[0].ID)
	assert.Equal(t, contentViewer, rows[0].ViewerUserID)
	assert.Equal(t, uint(1), rows[0].OrganizationID)
	assert.Equal(t, f.broadcastMessage, rows[0].MessageID)
	assert.Equal(t, "Complaint ticket 42", rows[0].Reason, "the reason is stored trimmed")
	encoded, err := json.Marshal(rows[0])
	require.NoError(t, err)
	for _, secret := range []string{"secret body", "customer@example.com", "Launch"} {
		assert.NotContains(t, string(encoded), secret, "the audit record never holds content or addresses")
	}
}

func TestOperatorContentConvertsHTMLOnlyMailToText(t *testing.T) {
	f := newContentFixture(t)
	recorder, body := f.read(t, 1, f.htmlOnly, `{"reason":"Review"}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := body["data"].(map[string]any)
	assert.Equal(t, "html", data["text_source"])
	assert.Equal(t, "Hello & welcome\nOne\nTwo\nLine\nbreak", data["text"])
	text := data["text"].(string)
	for _, hidden := range []string{"<", "tracker.example", "alert", "color:red", "Hidden title"} {
		assert.NotContains(t, text, hidden)
	}
}

// If the audit record cannot be written, no body leaves the API.
func TestOperatorContentAuditFailureReturnsNoBody(t *testing.T) {
	f := newContentFixture(t)
	require.NoError(t, f.repo.db.Exec(`ALTER TABLE mail_content_access_audits RENAME TO mail_content_access_audits_off`).Error)
	recorder, body := f.read(t, 1, f.broadcastMessage, `{"reason":"Complaint"}`)
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "secret body")
	assert.Nil(t, body["data"])
	require.NoError(t, f.repo.db.Exec(`ALTER TABLE mail_content_access_audits_off RENAME TO mail_content_access_audits`).Error)
	assert.Empty(t, f.audits(t))
}

func TestOperatorContentRefusesMailThatIsNotABroadcast(t *testing.T) {
	f := newContentFixture(t)
	inbound := storePlatformMessage(t, f.repo, f.north.ID, "inbound", "received", "Private", metricsDay.Add(12*time.Hour))
	synced := storePlatformMessage(t, f.repo, f.north.ID, "outbound", "sent", "Synced", metricsDay.Add(12*time.Hour))
	require.NoError(t, f.repo.db.Model(&MailMessagePO{}).Where("id = ?", synced).Update("source", "gmail").Error)
	storeBroadcastMessage(t, f.metricsFixture, f.broadcast, 3, synced)

	for name, messageID := range map[string]uint{
		"ordinary business outbound": f.sentB,
		"private inbox mail":         inbound,
		"synced external copy":       synced,
	} {
		recorder, body := f.read(t, 1, messageID, `{"reason":"Complaint"}`)
		assert.Equal(t, http.StatusForbidden, recorder.Code, name)
		assert.Equal(t, domain.CodeOperatorContentNotEligible, body["error_code"], name)
		assert.NotContains(t, recorder.Body.String(), "secret body", name)
	}
	assert.Empty(t, f.audits(t), "a refused read writes no access record")
}

func TestOperatorContentRefusesAnotherOrganizationsID(t *testing.T) {
	f := newContentFixture(t)
	// A broadcast of organization 2 that points at organization 1's message must not make it readable.
	foreign := &MailBroadcastPO{OrganizationID: 2, Name: "Other", MailboxID: f.south.ID, TemplateID: 1, ListID: 1, Status: "sent", CreatedBy: 1}
	require.NoError(t, f.repo.db.Create(foreign).Error)
	storeBroadcastMessage(t, f.metricsFixture, foreign.ID, 9, f.sentB)

	for name, path := range map[string][2]uint{
		"organization 2 asks for organization 1's broadcast": {2, f.broadcastMessage},
		"unknown organization":                               {99, f.broadcastMessage},
		"unknown message":                                    {1, 999999},
	} {
		recorder, body := f.read(t, path[0], path[1], `{"reason":"Complaint"}`)
		assert.Equal(t, http.StatusNotFound, recorder.Code, name)
		assert.Equal(t, domain.CodeMailMessageNotFound, body["error_code"], name)
	}
	recorder, body := f.read(t, 1, f.sentB, `{"reason":"Complaint"}`)
	assert.Equal(t, http.StatusForbidden, recorder.Code, "another organization's broadcast row does not qualify a message")
	assert.Equal(t, domain.CodeOperatorContentNotEligible, body["error_code"])
	assert.Empty(t, f.audits(t))
}

func TestOperatorContentNeedsABoundedReason(t *testing.T) {
	f := newContentFixture(t)
	for name, payload := range map[string]string{
		"missing":       `{}`,
		"blank":         `{"reason":"   "}`,
		"too long":      `{"reason":"` + strings.Repeat("x", 501) + `"}`,
		"control chars": `{"reason":"a\u0000b"}`,
	} {
		recorder, body := f.read(t, 1, f.broadcastMessage, payload)
		assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code, name)
		assert.Equal(t, "COMMON.VALIDATION_FAILED", body["error_code"], name)
		assert.NotContains(t, recorder.Body.String(), "secret body", name)
	}
	recorder, _ := f.read(t, 1, f.broadcastMessage, `{"reason":"`+strings.Repeat("é", 500)+`"}`)
	assert.Equal(t, http.StatusOK, recorder.Code, "500 characters is the limit, not 500 bytes")
	recorder, _ = f.read(t, 1, f.broadcastMessage, `{"reason":"Ticket 7\nfollow-up"}`)
	assert.Equal(t, http.StatusOK, recorder.Code, "line breaks are allowed")
	assert.Len(t, f.audits(t), 2)
}

func TestOperatorOutboundLogMarksBroadcastMessages(t *testing.T) {
	f := newContentFixture(t)
	rows, _, err := f.handler.outbound(context.Background(), outboundFilter{Day: metricsDay}, 1, 20)
	require.NoError(t, err)
	broadcasts := map[uint]*uint{}
	for _, row := range rows {
		broadcasts[row.ID] = row.BroadcastID
	}
	require.NotNil(t, broadcasts[f.broadcastMessage])
	assert.Equal(t, f.broadcast, *broadcasts[f.broadcastMessage])
	assert.Nil(t, broadcasts[f.sentB], "ordinary outbound has no broadcast")
	assert.Nil(t, broadcasts[f.failed])
}

func TestOperatorContentAccessAuditsFilter(t *testing.T) {
	f := newContentFixture(t)
	at := time.Date(2026, 11, 19, 8, 0, 0, 0, time.UTC)
	rows := []MailContentAccessAuditPO{
		{ViewerUserID: 42, OrganizationID: 1, MessageID: 10, Reason: "first", AccessedAt: at},
		{ViewerUserID: 43, OrganizationID: 1, MessageID: 11, Reason: "second", AccessedAt: at.Add(time.Hour)},
		{ViewerUserID: 42, OrganizationID: 2, MessageID: 12, Reason: "third", AccessedAt: at.Add(2 * time.Hour)},
	}
	require.NoError(t, f.repo.db.Create(&rows).Error)
	get := func(query string) (int, map[string]any) {
		recorder := httptest.NewRecorder()
		f.engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/content-access-audits"+query, nil))
		var body map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		return recorder.Code, body
	}
	reasons := func(body map[string]any) []string {
		var out []string
		for _, row := range body["data"].([]any) {
			out = append(out, row.(map[string]any)["reason"].(string))
		}
		return out
	}

	code, body := get("")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"third", "second", "first"}, reasons(body), "newest first")
	first := body["data"].([]any)[0].(map[string]any)
	assert.Equal(t, "South", first["organization_name"])
	assert.EqualValues(t, 42, first["viewer_user_id"])
	assert.EqualValues(t, 12, first["message_id"])

	_, body = get("?organization_id=1")
	assert.Equal(t, []string{"second", "first"}, reasons(body))
	_, body = get("?viewer_user_id=42")
	assert.Equal(t, []string{"third", "first"}, reasons(body))
	_, body = get("?from=2026-11-19T09:00:00Z&to=2026-11-19T10:00:00Z")
	assert.Equal(t, []string{"second"}, reasons(body), "from is inclusive and to exclusive")
	_, body = get("?per_page=1&page=2")
	assert.Equal(t, []string{"second"}, reasons(body))
	assert.EqualValues(t, 3, body["meta"].(map[string]any)["total"])
	_, body = get("?organization_id=3")
	assert.Empty(t, body["data"])

	for _, query := range []string{"?from=yesterday", "?organization_id=0", "?viewer_user_id=x", "?per_page=101",
		"?from=2026-11-19T10:00:00Z&to=2026-11-19T09:00:00Z"} {
		code, body = get(query)
		assert.Equal(t, http.StatusBadRequest, code, query)
		assert.Equal(t, "COMMON.INVALID_INPUT", body["error_code"], query)
	}
}

func TestReadableHTMLText(t *testing.T) {
	assert.Equal(t, "", readableHTMLText(""))
	assert.Equal(t, "Title\nParagraph one\nCell A Cell B",
		readableHTMLText("<h1>Title</h1><p>Paragraph   one</p><table><tr><td>Cell A</td><td>Cell B</td></tr></table>"))
	assert.Equal(t, "a\nb", readableHTMLText("<p>a</p><br><br><br><p>b</p>"), "empty lines are dropped")
	assert.Equal(t, "kept", readableHTMLText("<noscript>gone</noscript><template>gone</template>kept<!-- gone -->"))
}
