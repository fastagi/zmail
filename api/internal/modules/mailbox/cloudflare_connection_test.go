package mailbox

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/fastagi/zmail/api/internal/capabilities/crypto"
	"github.com/fastagi/zmail/api/internal/domain"
)

type tenantConnectionRepository struct {
	*fakeRepository
	connection *CloudflareConnection
}

func (r *tenantConnectionRepository) findConnection(_ context.Context, org uint, id string) (*CloudflareConnection, error) {
	c := r.connection
	if c == nil || (org != 0 && org != c.OrganizationID) || (id != "" && id != c.ID) {
		return nil, nil
	}
	return c, nil
}
func (*tenantConnectionRepository) insertConnection(context.Context, *CloudflareConnection) error {
	panic("unused")
}
func (*tenantConnectionRepository) revokeConnection(context.Context, uint, string) error {
	panic("unused")
}

func TestOrganizationWorkerRejectsOtherDomainsAndRevokedSignatures(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	m := f.mailbox(t, 7, "hello")
	other := f.mailbox(t, 8, "other")
	orgSecret := "a-different-worker-signature-key-for-tests"
	s := f.service
	s.encryptor = crypto.NewAESEncryptorFromString("local-encryption-key-for-isolation-tests")
	cipher, err := s.encryptor.EncryptString(orgSecret)
	if err != nil {
		t.Fatal(err)
	}
	id := "cf-test"
	row := &CloudflareConnection{ID: id, OrganizationID: 7, SigningCipher: cipher}
	store := &tenantConnectionRepository{f.repo, row}
	s.repo = store
	s.domains = &fakeDomains{domains: map[uint]*domain.MailDomain{1: {ID: 1, Name: "mail.example", OrganizationID: 7, CloudflareConnectionID: &id}}}
	raw := inboundRaw("tenant@sender.test")
	send := func(to, connection, secret string) error {
		header := fmt.Sprintf("t=%d,v1=%s", f.now.Unix(), signInbound(secret, f.now.Unix(), to, raw))
		_, _, err := s.ReceiveInbound(ctx, InboundDelivery{ConnectionID: connection, Recipient: to, Signature: header, Raw: raw})
		return err
	}
	if err := send(m.Address, id, orgSecret); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][3]string{{other.Address, id, orgSecret}, {m.Address, "", testInboundSecret}, {m.Address, id, testInboundSecret}, {m.Address, "other", orgSecret}} {
		if err := send(args[0], args[1], args[2]); !errors.Is(err, ErrMailInboundSignature) {
			t.Fatalf("unexpected authorization result: %v", err)
		}
	}
	store.connection = nil
	if err := send(m.Address, id, orgSecret); !errors.Is(err, ErrMailInboundSignature) {
		t.Fatalf("revoked worker: %v", err)
	}
	if len(f.repo.messages) != 1 {
		t.Fatalf("stored %d messages", len(f.repo.messages))
	}
}
