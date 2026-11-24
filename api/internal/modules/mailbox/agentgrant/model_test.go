package agentgrant

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestMailAgentGrantPORoundtrip(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("UTC+8", 8*3600)
	expires := time.Date(2026, 8, 1, 12, 0, 0, 0, loc)
	revoked := time.Date(2026, 7, 15, 9, 30, 0, 0, loc)
	created := time.Date(2026, 7, 1, 8, 0, 0, 0, loc)
	updated := time.Date(2026, 7, 2, 8, 0, 0, 0, loc)
	revokedBy := uint(99)

	orig := &domain.MailAgentGrant{
		ID:             12,
		OrganizationID: 3,
		MailboxID:      4,
		APIKeyID:       5,
		KeyUserID:      6,
		GrantedBy:      7,
		CanDraft:       true,
		RevealSecrets:  false,
		SenderFilters:  []string{"billing@example.com", "@partner.org"},
		LabelIDs:       []uint{10, 20},
		ExpiresAt:      expires,
		RevokedAt:      &revoked,
		RevokedBy:      &revokedBy,
		RevokeReason:   domain.MailAgentRevokedByPerson,
		CreatedAt:      created,
		UpdatedAt:      updated,
	}

	po, err := newMailAgentGrantPO(orig)
	if err != nil {
		t.Fatalf("newMailAgentGrantPO failed: %v", err)
	}
	if po.TableName() != "mail_agent_grants" {
		t.Fatalf("unexpected TableName: %q", po.TableName())
	}

	got := po.toDomain()
	if got.ID != orig.ID || got.OrganizationID != orig.OrganizationID || got.MailboxID != orig.MailboxID ||
		got.APIKeyID != orig.APIKeyID || got.KeyUserID != orig.KeyUserID || got.GrantedBy != orig.GrantedBy ||
		got.CanDraft != orig.CanDraft || got.RevealSecrets != orig.RevealSecrets ||
		got.RevokeReason != orig.RevokeReason {
		t.Fatalf("roundtrip scalar fields mismatch: got %+v, want %+v", got, orig)
	}
	if !slices.Equal(got.SenderFilters, orig.SenderFilters) {
		t.Fatalf("SenderFilters mismatch: got %v, want %v", got.SenderFilters, orig.SenderFilters)
	}
	if !slices.Equal(got.LabelIDs, orig.LabelIDs) {
		t.Fatalf("LabelIDs mismatch: got %v, want %v", got.LabelIDs, orig.LabelIDs)
	}
	if !got.ExpiresAt.Equal(expires.UTC()) || got.ExpiresAt.Location() != time.UTC {
		t.Fatalf("ExpiresAt not normalized to UTC: %v", got.ExpiresAt)
	}
	if got.RevokedAt == nil || !got.RevokedAt.Equal(revoked.UTC()) || got.RevokedAt.Location() != time.UTC {
		t.Fatalf("RevokedAt not normalized to UTC: %v", got.RevokedAt)
	}
	if got.RevokedBy == nil || *got.RevokedBy != revokedBy {
		t.Fatalf("RevokedBy mismatch: %v", got.RevokedBy)
	}
}

func TestMailAgentGrantPONilSlicesAndMalformedJSON(t *testing.T) {
	t.Parallel()

	po, err := newMailAgentGrantPO(&domain.MailAgentGrant{
		ID:        1,
		ExpiresAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("newMailAgentGrantPO failed: %v", err)
	}
	if po.SenderFilters != "[]" || po.LabelIDs != "[]" {
		t.Fatalf("expected empty JSON arrays, got senders=%q labels=%q", po.SenderFilters, po.LabelIDs)
	}

	malformed := &MailAgentGrantPO{
		ID:            2,
		SenderFilters: "{not-json",
		LabelIDs:      "bad",
		ExpiresAt:     time.Now().UTC(),
	}
	got := malformed.toDomain()
	if got.SenderFilters == nil || len(got.SenderFilters) != 0 {
		t.Fatalf("expected empty non-nil SenderFilters, got %#v", got.SenderFilters)
	}
	if got.LabelIDs == nil || len(got.LabelIDs) != 0 {
		t.Fatalf("expected empty non-nil LabelIDs, got %#v", got.LabelIDs)
	}
	if got.RevokedAt != nil {
		t.Fatalf("expected nil RevokedAt, got %v", got.RevokedAt)
	}
}

func TestOtherPOsToDomainAndTableNames(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("UTC-5", -5*3600)
	ts := time.Date(2026, 6, 10, 10, 0, 0, 0, loc)

	eventPO := MailAgentEventPO{
		ID: 1, GrantID: 2, OrganizationID: 3, MailboxID: 4, MessageID: 5,
		Type: string(domain.MailAgentEventMessageReceived), CreatedAt: ts,
	}
	if eventPO.TableName() != "mail_agent_events" {
		t.Fatalf("unexpected event TableName: %q", eventPO.TableName())
	}
	event := eventPO.toDomain()
	if event.ID != 1 || event.Type != domain.MailAgentEventMessageReceived || event.CreatedAt.Location() != time.UTC {
		t.Fatalf("unexpected event domain: %+v", event)
	}

	auditPO := MailAgentAuditPO{
		ID: 6, OrganizationID: 3, MailboxID: 4, GrantID: 2, APIKeyID: 9, MessageID: 5,
		Action: string(domain.MailAgentActionRead), CreatedAt: ts,
	}
	if auditPO.TableName() != "mail_agent_audit" {
		t.Fatalf("unexpected audit TableName: %q", auditPO.TableName())
	}
	audit := auditPO.toDomain()
	if audit.ID != 6 || audit.Action != domain.MailAgentActionRead || audit.CreatedAt.Location() != time.UTC {
		t.Fatalf("unexpected audit domain: %+v", audit)
	}

	draftPO := MailAgentDraftPO{
		ID: 8, OrganizationID: 3, MailboxID: 4, GrantID: 2, MessageID: 5, APIKeyID: 9,
		TextBody: "hello", HTMLBody: "<p>hello</p>", Status: "pending", CreatedAt: ts, UpdatedAt: ts,
	}
	if draftPO.TableName() != "mail_agent_drafts" {
		t.Fatalf("unexpected draft TableName: %q", draftPO.TableName())
	}
	draft := draftPO.toDomain()
	if draft.ID != 8 || draft.TextBody != "hello" || draft.HTMLBody != "<p>hello</p>" || draft.CreatedAt.Location() != time.UTC {
		t.Fatalf("unexpected draft domain: %+v", draft)
	}
}

func TestDecodeAgentKeyScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "json array", input: `["mail:agent", "mail:read"]`, want: []string{"mail:agent", "mail:read"}},
		{name: "invalid json array", input: `[invalid`, want: nil},
		{name: "comma separated", input: " mail:agent , , mail:send ", want: []string{"mail:agent", "mail:send"}},
		{name: "empty", input: "   ", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decodeAgentKeyScopes(tc.input)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("decodeAgentKeyScopes(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestRepositoryNilDBServiceUnavailable(t *testing.T) {
	t.Parallel()

	repo := NewRepository(nil, errors.New("mailbox not found"))
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := repo.FindAgentKey(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindAgentKey err = %v", err)
	}
	if err := repo.CreateAgentGrant(ctx, &domain.MailAgentGrant{}, 5); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateAgentGrant err = %v", err)
	}
	if _, err := repo.FindAgentGrant(ctx, 1, 2, nil); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindAgentGrant err = %v", err)
	}
	if _, _, err := repo.ListAgentGrants(ctx, 1, nil, 1, 20); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListAgentGrants err = %v", err)
	}
	if _, err := repo.ListLiveAgentGrantsOfMailbox(ctx, 1, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListLiveAgentGrantsOfMailbox err = %v", err)
	}
	if _, err := repo.ListLiveAgentGrantsOfKey(ctx, 1, 2, now); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListLiveAgentGrantsOfKey err = %v", err)
	}
	if _, err := repo.RevokeAgentGrants(ctx, domain.MailAgentGrantRevocation{OrganizationID: 1, GrantID: 2}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("RevokeAgentGrants err = %v", err)
	}
	if err := repo.AppendAgentEvents(ctx, 1, []*domain.MailAgentEvent{{GrantID: 1}}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("AppendAgentEvents err = %v", err)
	}
	if _, err := repo.ListAgentEvents(ctx, []uint{1}, 0, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListAgentEvents err = %v", err)
	}
	if _, err := repo.ListAgentMessageEvents(ctx, []uint{1}, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListAgentMessageEvents err = %v", err)
	}
	if err := repo.AddAgentAudit(ctx, &domain.MailAgentAuditEntry{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("AddAgentAudit err = %v", err)
	}
	if _, err := repo.ListAgentAudit(ctx, 1, nil, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListAgentAudit err = %v", err)
	}
	if _, err := repo.SaveAgentDraft(ctx, &domain.MailAgentDraft{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SaveAgentDraft err = %v", err)
	}
	if _, _, err := repo.ListAgentDrafts(ctx, 1, 1, 20); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListAgentDrafts err = %v", err)
	}
	if err := repo.DeleteAgentDrafts(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeleteAgentDrafts err = %v", err)
	}
	if _, err := repo.PruneAgentAudit(ctx, now, 100); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("PruneAgentAudit err = %v", err)
	}
	if _, err := repo.PruneAgentEvents(ctx, now, 100); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("PruneAgentEvents err = %v", err)
	}
}
