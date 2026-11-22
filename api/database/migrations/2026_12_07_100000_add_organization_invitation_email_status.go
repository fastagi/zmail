package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_07_100000_add_organization_invitation_email_status", &addOrganizationInvitationEmailStatus{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addOrganizationInvitationEmailStatus struct {
	migration.BaseMigration
}

// Up records the latest email attempt on each invitation: email_send_status is pending until the
// synchronous attempt reports accepted_by_provider, failed, or not_configured, and
// email_attempted_at is when it finished. Existing rows keep NULL, meaning the attempt predates
// tracking.
func (m *addOrganizationInvitationEmailStatus) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations ADD COLUMN email_send_status varchar(24)`,
		`ALTER TABLE organization_invitations ADD COLUMN email_attempted_at timestamptz`,
		`ALTER TABLE organization_invitations ADD CONSTRAINT organization_invitations_email_send_status_check
		    CHECK ((email_send_status IN ('pending', 'accepted_by_provider', 'failed', 'not_configured')))`,
	)
}

// Down removes the email attempt columns.
func (m *addOrganizationInvitationEmailStatus) Down(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE organization_invitations DROP CONSTRAINT IF EXISTS organization_invitations_email_send_status_check`,
		`ALTER TABLE organization_invitations DROP COLUMN IF EXISTS email_attempted_at`,
		`ALTER TABLE organization_invitations DROP COLUMN IF EXISTS email_send_status`,
	)
}
