package mailbox

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestRevealMessageReturnsTheOriginalAndLogsIt(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	handler := NewHandler(fixture.service)
	fixture.router.GET("/mailboxes/:mailbox_id/messages/:message_id", handler.GetMessage)
	fixture.router.POST("/mailboxes/:mailbox_id/messages/:message_id/reveal", handler.RevealMessage)
	mailbox, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("signup"))
	if err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	received := &domain.MailMessage{
		MailboxID: mailbox.ID, Direction: domain.MailDirectionInbound, Status: domain.MailStatusReceived,
		FromAddress: "noreply@medium.com", To: []string{mailbox.Address}, Subject: "你的 Medium 验证码",
		TextBody: "验证码 482913", HTMLBody: "<p>验证码 <b>482913</b></p>", DedupeKey: "code",
		CreatedAt: time.Unix(1_800_000_000, 0).UTC(),
	}
	if _, err = fixture.repo.CreateInboundMessage(t.Context(), received); err != nil {
		t.Fatalf("CreateInboundMessage: %v", err)
	}
	path := fmt.Sprintf("/mailboxes/%d/messages/%d", mailbox.ID, received.ID)

	recorder, body := fixture.do(http.MethodGet, path, "")
	detail, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || detail["secrets_masked"] != true ||
		strings.Contains(recorder.Body.String(), "482913") {
		t.Fatalf("GET detail = %d %s", recorder.Code, recorder.Body.String())
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	recorder, body = fixture.do(http.MethodPost, path+"/reveal", "")
	revealed, _ := body["data"].(map[string]any)
	if recorder.Code != http.StatusOK || revealed["text_body"] != "验证码 482913" ||
		revealed["html_body"] != "<p>验证码 <b>482913</b></p>" || revealed["secrets_masked"] != false {
		t.Fatalf("POST reveal = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("reveal headers = %v", recorder.Header())
	}
	line := logs.String()
	if !strings.Contains(line, `"msg":"mail.secret_revealed"`) ||
		!strings.Contains(line, fmt.Sprintf(`"user_id":%d`, handlerUser)) ||
		!strings.Contains(line, fmt.Sprintf(`"message_id":%d`, received.ID)) || strings.Contains(line, "482913") {
		t.Fatalf("reveal log = %s", line)
	}

	missing, _ := fixture.do(http.MethodPost, fmt.Sprintf("/mailboxes/%d/messages/%d/reveal", mailbox.ID, received.ID+999), "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing reveal = %d", missing.Code)
	}
}
