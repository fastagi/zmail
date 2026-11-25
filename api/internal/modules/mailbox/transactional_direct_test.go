package mailbox

import (
	"context"
	"errors"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

func TestParseDirectSender(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantAddr string
		wantName string
		wantOK   bool
	}{
		{name: "bare address", raw: "noreply@kfk.im", wantAddr: "noreply@kfk.im", wantName: "", wantOK: true},
		{name: "display name and address", raw: "Keepkite <noreply@kfk.im>", wantAddr: "noreply@kfk.im", wantName: "Keepkite", wantOK: true},
		{name: "quoted display name", raw: `"Keepkite Team" <NOREPLY@KFK.IM>`, wantAddr: "noreply@kfk.im", wantName: "Keepkite Team", wantOK: true},
		{name: "empty", raw: "", wantOK: false},
		{name: "not an address", raw: "just-text", wantOK: false},
		{name: "crlf injection", raw: "Keepkite\r\n <noreply@kfk.im>", wantOK: false},
		{name: "multiple addresses", raw: "a@kfk.im, b@kfk.im", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, name, ok := parseDirectSender(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseDirectSender(%q) ok=%v, want %v", tt.raw, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if addr != tt.wantAddr || name != tt.wantName {
				t.Fatalf("parseDirectSender(%q) = (%q, %q), want (%q, %q)", tt.raw, addr, name, tt.wantAddr, tt.wantName)
			}
		})
	}
}

func TestSendDirectTransactionalFlowAndRestrictions(t *testing.T) {
	f := newTxFixture(t, func(s *Settings) {
		s.Provider = config.MailboxProviderCloudflare
		s.Domain = "shared.zmail.im"
	})
	if err := f.db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
		VALUES (10, 'kfk.im', 'zone-kfk', 'kfk.im', 'verified', ?, now(), now())`, txUser).Error; err != nil {
		t.Fatalf("seed verified domain: %v", err)
	}
	if err := f.db.Exec(`INSERT INTO mail_domains (organization_id, name, zone_id, zone_name, status, created_by, created_at, updated_at)
		VALUES (10, 'pending.kfk.im', 'zone-pend', 'pending.kfk.im', 'pending', ?, now(), now())`, txUser).Error; err != nil {
		t.Fatalf("seed pending domain: %v", err)
	}

	ownerActor := TransactionalActor{
		OrganizationID: 10,
		UserID:         txUser,
		Mode:           domain.APIKeyModeLive,
		Viewer:         viewerOf(10, txUser, domain.OrganizationRoleOwner),
	}

	// 1. Direct send with display name and omitted Idempotency-Key delivers via transport.
	msg, created, err := f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:    "Keepkite <noreply@kfk.im>",
		To:      "alice@example.com",
		Subject: "Welcome to Keepkite",
		HTML:    "<p>Hello Alice</p>",
		Text:    "Hello Alice",
	})
	if err != nil {
		t.Fatalf("SendDirectTransactional: %v", err)
	}
	if !created || msg.ID == 0 {
		t.Fatalf("expected created message with ID, got created=%v id=%d", created, msg.ID)
	}
	if msg.FromAddress != "noreply@kfk.im" || msg.FromName != "Keepkite" {
		t.Fatalf("unexpected sender: %q / %q", msg.FromAddress, msg.FromName)
	}
	if msg.TemplateID != nil || msg.TemplateKey != "" {
		t.Fatalf("expected nil template for direct send, got id=%v key=%q", msg.TemplateID, msg.TemplateKey)
	}
	if msg.IdempotencyKey == nil || *msg.IdempotencyKey == "" {
		t.Fatalf("expected auto-generated idempotency key")
	}
	if len(f.transport.sent) != 1 || f.transport.sent[0].Subject != "Welcome to Keepkite" {
		t.Fatalf("expected 1 sent transport message, got %d", len(f.transport.sent))
	}

	// 2. Explicit Idempotency-Key replays on identical payload and conflicts on different payload.
	first, created, err := f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:           "noreply@kfk.im",
		To:             "bob@example.com",
		Subject:        "Receipt",
		Text:           "Paid $10",
		IdempotencyKey: "receipt-1",
	})
	if err != nil || !created {
		t.Fatalf("first idempotent direct send: created=%v err=%v", created, err)
	}
	replay, created, err := f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:           "noreply@kfk.im",
		To:             "bob@example.com",
		Subject:        "Receipt",
		Text:           "Paid $10",
		IdempotencyKey: "receipt-1",
	})
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("idempotent replay: created=%v id=%d want=%d err=%v", created, replay.ID, first.ID, err)
	}
	_, _, err = f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:           "noreply@kfk.im",
		To:             "bob@example.com",
		Subject:        "Different Receipt",
		Text:           "Paid $20",
		IdempotencyKey: "receipt-1",
	})
	if !errors.Is(err, ErrMailIdempotencyConflict) {
		t.Fatalf("expected ErrMailIdempotencyConflict, got %v", err)
	}

	// 3. Test mode simulates delivery without calling transport.
	beforeSent := len(f.transport.sent)
	testActor := ownerActor
	testActor.Mode = domain.APIKeyModeTest
	simMsg, created, err := f.service.SendDirectTransactional(context.Background(), testActor, TransactionalDirectSendInput{
		From:    "noreply@kfk.im",
		To:      "carol@example.com",
		Subject: "Test email",
		HTML:    "<p>Simulated</p>",
	})
	if err != nil || !created || !simMsg.Simulated {
		t.Fatalf("test mode direct send: created=%v simulated=%v err=%v", created, simMsg.Simulated, err)
	}
	if len(f.transport.sent) != beforeSent {
		t.Fatalf("test mode must not invoke transport")
	}

	// 4. Unverified domain is rejected.
	_, _, err = f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:    "noreply@pending.kfk.im",
		To:      "alice@example.com",
		Subject: "Blocked",
		Text:    "Nope",
	})
	if !errors.Is(err, ErrMailDomainNotVerified) {
		t.Fatalf("expected ErrMailDomainNotVerified, got %v", err)
	}

	// 5. Restricted API key cannot send from disallowed domain or shared platform domain.
	restrictedCtx := domain.WithAPIKeyRestriction(context.Background(), domain.APIKeyRestriction{
		OrganizationID: 10,
		SendingDomains: []string{"kfk.im"},
	})
	_, _, err = f.service.SendDirectTransactional(restrictedCtx, ownerActor, TransactionalDirectSendInput{
		From:    "noreply@mail.example",
		To:      "alice@example.com",
		Subject: "Disallowed",
		Text:    "Nope",
	})
	if !errors.Is(err, domain.ErrAPIKeyDomainNotAllowed) {
		t.Fatalf("expected ErrAPIKeyDomainNotAllowed for unlisted domain, got %v", err)
	}
	_, _, err = f.service.SendDirectTransactional(restrictedCtx, ownerActor, TransactionalDirectSendInput{
		From:    "noreply@shared.zmail.im",
		To:      "alice@example.com",
		Subject: "Shared",
		Text:    "Nope",
	})
	if !errors.Is(err, domain.ErrAPIKeyDomainNotAllowed) {
		t.Fatalf("expected ErrAPIKeyDomainNotAllowed for shared domain, got %v", err)
	}

	// 6. Missing body fails validation.
	_, _, err = f.service.SendDirectTransactional(context.Background(), ownerActor, TransactionalDirectSendInput{
		From:    "noreply@kfk.im",
		To:      "alice@example.com",
		Subject: "Empty body",
	})
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected ValidationError for empty html/text, got %v", err)
	}
}
