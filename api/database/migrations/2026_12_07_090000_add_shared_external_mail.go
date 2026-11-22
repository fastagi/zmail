package migrations

import (
	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/internal/infra/migration"
)

func init() {
	register("2026_12_07_090000_add_shared_external_mail", &addSharedExternalMail{
		BaseMigration: migration.BaseMigration{UseTransaction: true},
	})
}

type addSharedExternalMail struct {
	migration.BaseMigration
}

// Up lets an organization own a connection, for a shared connected mailbox bound to a team account:
//
//   - external_mail_connections.shared marks an organization's connection. It has no owner, so the
//     per-member limit, the member's binding lock, and the revocation when a member leaves, which all
//     key on owner_user_id, never touch it; the CHECK keeps the two from being mixed.
//   - bound_by_user_id records who bound any connection, backfilled from the owner.
//   - idx_external_mail_connections_shared serves the per-organization count and listing.
//   - external_mail_authorizations.sharing holds the members of a shared sign-in until it completes.
//
// Both tables are bounded per member and per organization, and the defaults are constants that
// PostgreSQL 11+ records without rewriting the table.
func (m *addSharedExternalMail) Up(db *gorm.DB) error {
	return execStatements(db,
		`ALTER TABLE external_mail_connections
		  ADD COLUMN shared boolean NOT NULL DEFAULT false,
		  ADD COLUMN bound_by_user_id bigint REFERENCES users(id) ON DELETE SET NULL`,
		`UPDATE external_mail_connections SET bound_by_user_id = owner_user_id WHERE owner_user_id IS NOT NULL`,
		`ALTER TABLE external_mail_connections
		  ADD CONSTRAINT chk_external_mail_connections_shared CHECK (NOT shared OR owner_user_id IS NULL)`,
		`CREATE INDEX idx_external_mail_connections_shared ON external_mail_connections (organization_id) WHERE shared`,
		`ALTER TABLE external_mail_authorizations ADD COLUMN sharing jsonb`,
	)
}

// Down drops organization connections. It fails while one exists, which the older schema would
// read as a member's connection without an owner; disconnect and purge them first.
func (m *addSharedExternalMail) Down(db *gorm.DB) error {
	return execStatements(db,
		`DO $$
		BEGIN
		  IF EXISTS (SELECT 1 FROM external_mail_connections WHERE shared) THEN
		    RAISE EXCEPTION 'a shared external mail connection exists; the older schema cannot express it';
		  END IF;
		END $$`,
		`ALTER TABLE external_mail_authorizations DROP COLUMN IF EXISTS sharing`,
		`DROP INDEX IF EXISTS idx_external_mail_connections_shared`,
		`ALTER TABLE external_mail_connections
		  DROP CONSTRAINT IF EXISTS chk_external_mail_connections_shared,
		  DROP COLUMN IF EXISTS bound_by_user_id,
		  DROP COLUMN IF EXISTS shared`,
	)
}
