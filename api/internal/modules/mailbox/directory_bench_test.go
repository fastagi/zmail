package mailbox

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/fastagi/zmail/api/database/migrations"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/migration"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

const (
	directoryBenchMailboxes = 1_000
	directoryBenchAttempts  = 20_000
	directoryBenchOrg       = 20
)

// directoryScene is an organization of 1,000 shared mailboxes with five members each and 20,000
// outbound attempts in the last 24 hours, in its own schema migrated by the real migrations.
var directoryScene struct {
	once   sync.Once
	admin  string
	schema string
	db     *gorm.DB
	repo   *repository
	err    error
}

func dropDirectoryScene() {
	if directoryScene.db == nil {
		return
	}
	if pool, err := directoryScene.db.DB(); err == nil {
		_ = pool.Close()
	}
	admin, err := gorm.Open(postgres.Open(directoryScene.admin), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return
	}
	admin.Exec(`DROP SCHEMA IF EXISTS "` + directoryScene.schema + `" CASCADE`)
	if pool, err := admin.DB(); err == nil {
		_ = pool.Close()
	}
}

func openDirectoryScene(b *testing.B) *repository {
	b.Helper()
	dsn := strings.TrimSpace(os.Getenv(testplatform.TestPostgresDSNEnv))
	if dsn == "" {
		b.Skip(testplatform.TestPostgresDSNEnv + " is not set")
	}
	directoryScene.once.Do(func() { directoryScene.err = seedDirectoryScene(dsn) })
	if directoryScene.err != nil {
		b.Fatalf("seed the directory scene: %v", directoryScene.err)
	}
	return directoryScene.repo
}

func seedDirectoryScene(dsn string) error {
	quiet := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), quiet)
	if err != nil {
		return err
	}
	directoryScene.admin = dsn
	directoryScene.schema = fmt.Sprintf("bench_mail_directory_%d", os.Getpid())
	err = admin.Exec(`CREATE SCHEMA "` + directoryScene.schema + `"`).Error
	if pool, closeErr := admin.DB(); closeErr == nil {
		_ = pool.Close()
	}
	if err != nil {
		return err
	}
	scoped, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	query := scoped.Query()
	query.Set("search_path", directoryScene.schema)
	query.Set("timezone", "UTC")
	scoped.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: scoped.String(), PreferSimpleProtocol: true}), quiet)
	if err != nil {
		return err
	}
	directoryScene.db = db

	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	if _, err := migrator.Run(migration.MigratorOptions{}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	statements := []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at)
		  SELECT 900 + g, 'bench' || g, 'bench' || g || '@example.com', 'x', now(), now() FROM generate_series(0, 5) AS g`,
		fmt.Sprintf(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (%d, 'Bench', 'bench', 900, now(), now())`, directoryBenchOrg),
		fmt.Sprintf(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at)
		  SELECT 200 + g, %d, 900 + g, CASE WHEN g = 0 THEN 'owner' ELSE 'member' END, now(), now() FROM generate_series(0, 5) AS g`, directoryBenchOrg),
		// Every tenth mailbox is a sales mailbox, so the search matches about 100 of them.
		fmt.Sprintf(`INSERT INTO mailboxes (id, organization_id, created_by, address, display_name, type, created_at, updated_at)
		  SELECT g, %d, 900,
		    CASE WHEN g %% 10 = 0 THEN 'sales' || g ELSE 'box' || g END || '@mail.example',
		    'Mailbox ' || g, 'shared', now() - (%d - g) * interval '1 minute', now()
		  FROM generate_series(1, %d) AS g`, directoryBenchOrg, directoryBenchMailboxes, directoryBenchMailboxes),
		`SELECT setval(pg_get_serial_sequence('mailboxes', 'id'), (SELECT max(id) FROM mailboxes))`,
		fmt.Sprintf(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at)
		  SELECT m, 200 + u, 900 + u, 'send', 900, now(), now()
		  FROM generate_series(1, %d) AS m, generate_series(1, 5) AS u`, directoryBenchMailboxes),
		fmt.Sprintf(`INSERT INTO mail_messages (mailbox_id, direction, status, from_address, to_addresses, subject,
		    send_attempted_at, send_lane, thread_id, created_at, updated_at)
		  SELECT 1 + (g %% %d), 'outbound', 'sent', 'box@mail.example', '["x@example.com"]', 'Out ' || g,
		    now() - (g %% 1380) * interval '1 minute', CASE WHEN g %% 10 = 0 THEN 'broadcast' ELSE 'personal' END,
		    g, now(), now()
		  FROM generate_series(1, %d) AS g`, directoryBenchMailboxes, directoryBenchAttempts),
		`ANALYZE`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	directoryScene.repo = NewRepository(db)
	return nil
}

// BenchmarkMailboxDirectory1000 reads the first directory page (15 rows) of an organization with
// 1,000 mailboxes, without and with a search. Targets: p95 < 50 ms and < 100 ms.
func BenchmarkMailboxDirectory1000(b *testing.B) {
	repo := openDirectoryScene(b)
	settings := accessSettings()
	settings.MaxMailboxes = 5000
	svc := NewService(repo, newAccessMemberships(), nil, newFakeContacts(), &stubBroadcasts{}, nil,
		nil, nil, nil, &fakeTransport{}, &fakeNotifier{}, settings, nil, nil, nil, nil)
	admin := viewerOf(directoryBenchOrg, 900, domain.OrganizationRoleAdmin)
	ctx := context.Background()
	for _, scenario := range []struct{ name, query string }{{"first-page", ""}, {"search-sales", "sales"}} {
		b.Run(scenario.name, func(b *testing.B) {
			measure(b, func() error {
				page, err := svc.MailboxDirectory(ctx, admin, domain.MailboxDirectoryFilter{Query: scenario.query, Limit: 15})
				if err == nil && (len(page.Entries) != 15 || page.Entries[0].MemberCount != 5 || page.MailboxCount != directoryBenchMailboxes) {
					err = fmt.Errorf("page = %d entries, %d members, %d mailboxes", len(page.Entries), page.Entries[0].MemberCount, page.MailboxCount)
				}
				return err
			})
		})
	}
}
