package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_07_090000_add_organization_invitation_email_status", &addOrganizationInvitationEmailStatus{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addOrganizationInvitationEmailStatus struct {
	migration.BaseMigration
}

// Up records the outcome of the latest invitation email attempt, so managers can tell an unsent
// invitation from a sent one and resend it. Existing rows stay NULL (unknown). Both columns are
// nullable without defaults, so the ALTER is a catalog-only change.
func (m *addOrganizationInvitationEmailStatus) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations
		  ADD COLUMN email_send_status varchar(32)
		    CONSTRAINT organization_invitations_email_send_status_check
		    CHECK (email_send_status IN ('accepted_by_provider','failed','not_configured')),
		  ADD COLUMN email_attempted_at timestamptz`,
	)
}

// Down drops the email attempt columns.
func (m *addOrganizationInvitationEmailStatus) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations DROP COLUMN email_attempted_at, DROP COLUMN email_send_status`,
	)
}
