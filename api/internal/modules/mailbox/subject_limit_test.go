package mailbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Issue 110: the subject limit is 998 characters, whatever the script, never 998 bytes.
func TestSendSubjectLimitCountsCharacters(t *testing.T) {
	fixture := newHandlerFixture(t, membership(domain.OrganizationRoleMember))
	if _, err := fixture.service.CreateMailbox(t.Context(), as(7), sharedWithHandlerUser("helpdesk")); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}
	send := func(subject string) (int, map[string]any) {
		recorder, body := fixture.do(http.MethodPost, "/mailboxes/1/messages",
			fmt.Sprintf(`{"to":["bob@example.com"],"subject":%q,"text":"Body"}`, subject))
		return recorder.Code, body
	}
	for _, subject := range []string{strings.Repeat("中", 998), strings.Repeat("😀", 998), strings.Repeat("a", 998)} {
		code, body := send(subject)
		data, _ := body["data"].(map[string]any)
		if code != http.StatusCreated || data["subject"] != subject {
			t.Fatalf("%d-byte subject of 998 characters = %d %v; want 201 with the subject kept", len(subject), code, body["error_code"])
		}
	}
	for _, subject := range []string{strings.Repeat("中", 999), strings.Repeat("a", 999)} {
		if code, body := send(subject); code != http.StatusUnprocessableEntity || body["error_code"] != domain.CodeMailMessageInvalid {
			t.Fatalf("999-character subject = %d %v; want 422 %s", code, body["error_code"], domain.CodeMailMessageInvalid)
		}
	}
}

// The PostgreSQL column holds 998 characters, so a stored multi-byte subject at the limit is kept.
func TestStoredSubjectHoldsTheLimitInCharacters(t *testing.T) {
	fixture := newBroadcastFixture(t, 100)
	subject := strings.Repeat("中", 998)
	message, _, err := fixture.service.SendMessage(context.Background(), as(1), fixture.mailbox.ID, SendInput{
		To: []string{"bob@example.com"}, Subject: subject, Text: "Body",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	stored, err := fixture.repo.FindMessage(context.Background(), fixture.mailbox.ID, message.ID)
	if err != nil || stored.Subject != subject {
		t.Fatalf("stored subject = %d characters, %v; want 998", utf8.RuneCountInString(stored.Subject), err)
	}
}

func TestTemplateSubjectLimitCountsCharacters(t *testing.T) {
	fixture := newServiceFixture(t)
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), TemplateInput{
		Name: "Long", Subject: strings.Repeat("中", 998), Text: "Body",
	}); err != nil {
		t.Fatalf("CreateTemplate(998 characters): %v", err)
	}
	if _, err := fixture.service.CreateTemplate(context.Background(), as(1), TemplateInput{
		Name: "Longer", Subject: strings.Repeat("中", 999), Text: "Body",
	}); err == nil {
		t.Fatal("CreateTemplate(999 characters) succeeded; want invalid")
	}
}

// Received and synced subjects are cut to 998 characters, never inside a character.
func TestReceivedSubjectIsCutAtTheCharacterLimit(t *testing.T) {
	long := strings.Repeat("中", 1000)
	raw := "From: a@example.org\r\nTo: b@mail.example\r\nSubject: =?UTF-8?B?" +
		base64.StdEncoding.EncodeToString([]byte(long)) + "?=\r\n\r\nBody\r\n"
	parsed, err := parseMail([]byte(raw))
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	if parsed.Subject != strings.Repeat("中", 998) {
		t.Fatalf("subject = %d characters; want 998", utf8.RuneCountInString(parsed.Subject))
	}
	for _, value := range []string{"a" + strings.Repeat("中", 1000), strings.Repeat("😀", 999), "short"} {
		cut := truncateRunes(value, domain.MaxMailSubjectLength)
		if !utf8.ValidString(cut) || utf8.RuneCountInString(cut) > domain.MaxMailSubjectLength || !strings.HasPrefix(value, cut) {
			t.Fatalf("truncateRunes = %d characters, valid %v", utf8.RuneCountInString(cut), utf8.ValidString(cut))
		}
		if want := min(utf8.RuneCountInString(value), domain.MaxMailSubjectLength); utf8.RuneCountInString(cut) != want {
			t.Fatalf("truncateRunes kept %d characters; want %d", utf8.RuneCountInString(cut), want)
		}
	}
}
