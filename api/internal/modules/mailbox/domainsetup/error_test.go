package domainsetup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/mailtransport"
)

type stubNetError struct{}

func (stubNetError) Error() string   { return "connection reset" }
func (stubNetError) Timeout() bool   { return true }
func (stubNetError) Temporary() bool { return true }

type stubSQLStateError struct{ state string }

func (e stubSQLStateError) Error() string    { return "pg error " + e.state }
func (e stubSQLStateError) SQLState() string { return e.state }

func TestErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil returns empty", err: nil, want: ""},
		{name: "zone not found", err: fmt.Errorf("wrap: %w", mailtransport.ErrZoneNotFound), want: DomainErrorZoneNotFound},
		{name: "unauthorized status", err: &mailtransport.DomainError{Status: http.StatusUnauthorized}, want: DomainErrorPermissionDenied},
		{name: "forbidden status", err: &mailtransport.DomainError{Status: http.StatusForbidden}, want: DomainErrorPermissionDenied},
		{name: "cloudflare auth code 10000", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Code: 10000}, want: DomainErrorPermissionDenied},
		{name: "rate limited 429", err: &mailtransport.DomainError{Status: http.StatusTooManyRequests}, want: DomainErrorProviderUnavailable},
		{name: "server error 503", err: &mailtransport.DomainError{Status: http.StatusServiceUnavailable}, want: DomainErrorProviderUnavailable},
		{name: "enable email routing", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "enable email routing"}, want: DomainErrorRoutingRefused},
		{name: "enable email sending", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "enable email sending"}, want: DomainErrorSendingRefused},
		{name: "sending records", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "sending records"}, want: DomainErrorSendingRefused},
		{name: "list sending domains", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "list sending domains"}, want: DomainErrorSendingRefused},
		{name: "create record", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "create record"}, want: DomainErrorDNSRecordRefused},
		{name: "create routing rule", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "create routing rule"}, want: DomainErrorRoutingRuleRefused},
		{name: "other provider operation", err: &mailtransport.DomainError{Status: http.StatusBadRequest, Operation: "delete record"}, want: DomainErrorProviderRefused},
		{name: "context deadline exceeded", err: context.DeadlineExceeded, want: DomainErrorProviderUnavailable},
		{name: "network error", err: stubNetError{}, want: DomainErrorProviderUnavailable},
		{name: "generic error", err: errors.New("unexpected"), want: DomainErrorSetupFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ErrorCode(tc.err); got != tc.want {
				t.Fatalf("ErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestRepositoryHelpers(t *testing.T) {
	t.Parallel()

	if got := TruncateRunes("hello", 10); got != "hello" {
		t.Fatalf("TruncateRunes unchanged = %q", got)
	}
	if got := TruncateRunes("caf\u00e9!", 4); got != "caf\u00e9" {
		t.Fatalf("TruncateRunes multibyte = %q", got)
	}
	if got := escapeLike(`a%b_c\d`); got != `a\%b\_c\\d` {
		t.Fatalf("escapeLike = %q", got)
	}

	if !isUniqueViolation(gorm.ErrDuplicatedKey) || !isUniqueViolation(stubSQLStateError{state: "23505"}) || isUniqueViolation(errors.New("other")) {
		t.Fatalf("isUniqueViolation classification failed")
	}
	if !isForeignKeyViolation(gorm.ErrForeignKeyViolated) || !isForeignKeyViolation(stubSQLStateError{state: "23503"}) || isForeignKeyViolation(nil) || isForeignKeyViolation(errors.New("other")) {
		t.Fatalf("isForeignKeyViolation classification failed")
	}

	encoded, err := encodeNameServers([]string{"ns1.example.com", "ns2.example.com"})
	if err != nil {
		t.Fatalf("encodeNameServers failed: %v", err)
	}
	if got := decodeNameServers(encoded); !slices.Equal(got, []string{"ns1.example.com", "ns2.example.com"}) {
		t.Fatalf("decodeNameServers = %v", got)
	}
	if got := decodeNameServers("{invalid"); len(got) != 0 {
		t.Fatalf("decodeNameServers invalid JSON = %v", got)
	}

	var nilRow *mailboxRow
	if nilRow.toDomain() != nil {
		t.Fatalf("nil mailboxRow.toDomain should return nil")
	}
	orgID := uint(7)
	pending := "owner@example.com"
	row := &mailboxRow{ID: 3, OrganizationID: &orgID, Address: "ops@example.com", PendingOwnerEmail: &pending}
	box := row.toDomain()
	if box.OrganizationID != 7 || box.PendingOwnerEmail != pending || box.Origin != domain.MailboxOriginZmail {
		t.Fatalf("unexpected mailboxRow.toDomain: %+v", box)
	}
}

func TestRepositoryNilDBServiceUnavailable(t *testing.T) {
	t.Parallel()

	repo := NewRepository(nil, Errors{}, "zone:")
	ctx := context.Background()

	if err := repo.CreateDomain(ctx, &domain.MailDomain{}, 5); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateDomain err = %v", err)
	}
	if _, err := repo.ListDomains(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListDomains err = %v", err)
	}
	if _, err := repo.FindDomain(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindDomain err = %v", err)
	}
	if _, err := repo.FindDomainByName(ctx, "example.com"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindDomainByName err = %v", err)
	}
	if err := repo.UpdateDomain(ctx, &domain.MailDomain{}); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("UpdateDomain err = %v", err)
	}
	if err := repo.DeleteDomain(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("DeleteDomain err = %v", err)
	}
	if err := repo.SetMailboxRoutingRule(ctx, 1, "rule-1"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SetMailboxRoutingRule err = %v", err)
	}
	if err := repo.SetDomainCapability(ctx, 1, 2, domain.MailDomainSendReceive); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SetDomainCapability err = %v", err)
	}
	if err := repo.SetDefaultDomain(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("SetDefaultDomain err = %v", err)
	}
	if _, err := repo.CountMailboxesByDomain(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CountMailboxesByDomain err = %v", err)
	}
	if _, err := repo.ListMailboxesWithoutRoutingRule(ctx, 1, "example.com"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListMailboxesWithoutRoutingRule err = %v", err)
	}
	if err := repo.CreateZone(ctx, &domain.MailZone{}, 5); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("CreateZone err = %v", err)
	}
	if _, err := repo.FindZone(ctx, 1, 2); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindZone err = %v", err)
	}
	if _, err := repo.FindZoneByName(ctx, "example.com"); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("FindZoneByName err = %v", err)
	}
	if _, err := repo.ListZones(ctx, 1); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListZones err = %v", err)
	}
	if _, err := repo.ListPendingZones(ctx, 10); !errors.Is(err, domain.ErrServiceUnavailable) {
		t.Fatalf("ListPendingZones err = %v", err)
	}
}
