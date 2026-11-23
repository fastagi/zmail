package mailbox

import (
	"context"
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

// newTriageFixture is the PostgreSQL access scenario with the triage tables and the fake model
// configured; every switch starts off.
func newTriageFixture(t *testing.T) *accessFixture {
	t.Helper()
	repo, db := openRepository(t)
	if err := db.AutoMigrate(&MailTriagePO{}, &MailTriageSettingsPO{}); err != nil {
		t.Fatalf("migrate triage tables: %v", err)
	}
	createOwner(t, db, accessOrg)
	transport := &fakeTransport{}
	memberships := newAccessMemberships()
	settings := accessSettings()
	settings.Triage = config.AITriageConfig{Provider: config.AITriageProviderFake, DailyCap: 100}
	svc := NewService(repo, memberships, repo, repo, repo, repo,
		nil, nil, nil, transport, &fakeNotifier{}, settings, nil, nil, nil, nil)
	return seedAccessFixture(t, svc, repo, transport, memberships)
}

// triageRepo is the fixture's triage store.
func (f *accessFixture) triageRepo() domain.MailTriageRepository {
	return f.repo.(domain.MailTriageRepository)
}

// switchTriage sets the organization switch (with drafts) and the given mailboxes' switches
// directly in the store, without the service's permission checks.
func (f *accessFixture) switchTriage(t *testing.T, organization bool, mailboxes ...*domain.Mailbox) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.triageRepo().SaveTriageSettings(ctx, domain.MailTriageSettings{
		OrganizationID: accessOrg, Enabled: organization, DailyMessageCap: 50, DraftEnabled: organization,
	}, f.service.now()); err != nil {
		t.Fatalf("SaveTriageSettings: %v", err)
	}
	for _, mailbox := range mailboxes {
		if err := f.triageRepo().SetMailboxTriage(ctx, mailbox.ID, true); err != nil {
			t.Fatalf("SetMailboxTriage: %v", err)
		}
	}
}

// triageOf reads a message's triage row; nil without one.
func (f *accessFixture) triageOf(t *testing.T, messageID uint) *domain.MailTriage {
	t.Helper()
	triage, err := f.triageRepo().FindTriage(context.Background(), messageID)
	if err != nil {
		t.Fatalf("FindTriage: %v", err)
	}
	return triage
}
