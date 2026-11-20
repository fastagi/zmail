package mailbox

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/fastagi/zmail/api/database/migrations"
	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/migration"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// The unified inbox scene (design §14.3): one organization with 11 mailboxes. Mailbox 1 holds
// 100,000 messages, mailboxes 2–10 about 11,100 each, and mailbox 11, which the bench membership
// cannot read, 50,000.
const (
	inboxBenchReadable   = 200_000
	inboxBenchUnreadable = 50_000
	inboxBenchGrowth     = 450_000
	inboxBenchToday      = 2_000
	inboxBenchPageSize   = 20
	inboxBenchMembership = 100
	inboxBenchHidden     = 11
)

// inboxBenchMailboxes are the ten mailboxes the bench membership reads.
var inboxBenchMailboxes = []uint{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

// inboxScene is seeded once per test binary in its own schema; TestMain drops it.
var inboxScene struct {
	once   sync.Once
	grow   sync.Once
	admin  string
	schema string
	db     *gorm.DB
	repo   *repository
	now    time.Time
	err    error
}

func dropInboxScene() {
	if inboxScene.db == nil {
		return
	}
	if pool, err := inboxScene.db.DB(); err == nil {
		_ = pool.Close()
	}
	admin, err := gorm.Open(postgres.Open(inboxScene.admin), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return
	}
	admin.Exec(`DROP SCHEMA IF EXISTS "` + inboxScene.schema + `" CASCADE`)
	if pool, err := admin.DB(); err == nil {
		_ = pool.Close()
	}
}

func openInboxScene(b *testing.B) *repository {
	b.Helper()
	dsn := strings.TrimSpace(os.Getenv(testplatform.TestPostgresDSNEnv))
	if dsn == "" {
		b.Skip(testplatform.TestPostgresDSNEnv + " is not set")
	}
	inboxScene.once.Do(func() {
		started := time.Now()
		inboxScene.err = seedInboxScene(dsn)
		b.Logf("seeded %d readable and %d unreadable messages in %s",
			inboxBenchReadable, inboxBenchUnreadable, time.Since(started).Round(time.Millisecond))
	})
	if inboxScene.err != nil {
		b.Fatalf("seed the inbox scene: %v", inboxScene.err)
	}
	return inboxScene.repo
}

// inboxMessagesSQL inserts ids [first, last] into mailboxSQL (an expression of g). Threads of
// three; 70% inbound; 5% trash, 5% in a folder, 10% archived. Arrival times are a permutation of
// one-minute steps back from the bench instant, so every mailbox has mail at every age and the
// newest inboxBenchToday readable messages fall in the bench "today".
func inboxMessagesSQL(first, last int, mailboxSQL string, now time.Time) string {
	return fmt.Sprintf(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, from_name, to_addresses,
	  subject, text_body, placement, folder_id, trashed_at, thread_id, rfc_message_id, attachment_count, created_at, updated_at)
	SELECT g, %[3]s,
	  CASE WHEN g %% 10 < 7 THEN 'inbound' ELSE 'outbound' END,
	  CASE WHEN g %% 10 < 7 THEN 'received' ELSE 'sent' END,
	  'sender' || (g %% 5000) || '@example.com', 'Sender ' || (g %% 5000), '["team@mail.example"]',
	  'Order ' || md5(g::text),
	  'Hello, this is message ' || g || ' about ' || md5((g * 7)::text) || ' and ' || md5((g * 13)::text),
	  CASE WHEN g %% 20 = 0 THEN 'trash' WHEN g %% 20 = 1 THEN 'folder' WHEN g %% 10 = 2 THEN 'archive' ELSE 'default' END,
	  CASE WHEN g %% 20 = 1 THEN %[3]s END,
	  CASE WHEN g %% 20 = 0 THEN '%[4]s'::timestamptz END,
	  ((g - 1) / 3) * 3 + 1,
	  'm' || g || '@example.com',
	  CASE WHEN g %% 15 = 0 THEN 1 ELSE 0 END,
	  '%[4]s'::timestamptz - ((g::bigint * 7919) %% %[5]d) * interval '1 minute',
	  '%[4]s'::timestamptz - ((g::bigint * 7919) %% %[5]d) * interval '1 minute'
	FROM generate_series(%[1]d, %[2]d) AS g`, first, last, mailboxSQL, now.Format(time.RFC3339Nano), inboxBenchReadable)
}

func seedInboxScene(dsn string) error {
	quiet := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), quiet)
	if err != nil {
		return err
	}
	inboxScene.admin = dsn
	inboxScene.schema = fmt.Sprintf("bench_mail_inbox_%d", os.Getpid())
	err = admin.Exec(`CREATE SCHEMA "` + inboxScene.schema + `"`).Error
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
	query.Set("search_path", inboxScene.schema)
	query.Set("timezone", "UTC")
	scoped.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: scoped.String(), PreferSimpleProtocol: true}), quiet)
	if err != nil {
		return err
	}
	inboxScene.db = db
	inboxScene.now = time.Now().UTC().Truncate(time.Minute)

	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	if _, err := migrator.Run(migration.MigratorOptions{}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	now := inboxScene.now
	statements := []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'bench', 'bench@example.com', 'x', now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Bench', 'bench', 901, now(), now())`,
		`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (100, 10, 901, 'member', now(), now())`,
		`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at)
		  SELECT n, 10, 901, 'box' || n || '@mail.example', now(), now() FROM generate_series(1, 11) AS n`,
		`SELECT setval(pg_get_serial_sequence('mailboxes', 'id'), 11)`,
		`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at)
		  SELECT n, 100, 901, 'view', 901, now(), now() FROM generate_series(1, 10) AS n`,
		`INSERT INTO mail_folders (id, mailbox_id, name, created_at, updated_at)
		  SELECT n, n, 'Clients', now(), now() FROM generate_series(1, 11) AS n`,
		// Mailbox 1 takes ids 1–100,000; mailboxes 2–10 take blocks of 11,112 of the next 100,000.
		inboxMessagesSQL(1, inboxBenchReadable,
			"CASE WHEN g <= 100000 THEN 1 ELSE 2 + (g - 100001) / 11112 END", now),
		inboxMessagesSQL(inboxBenchReadable+1, inboxBenchReadable+inboxBenchUnreadable, "11", now),
		`SELECT setval(pg_get_serial_sequence('mail_messages', 'id'), (SELECT max(id) FROM mail_messages))`,
		// The bench membership has read 80% of the readable mail and starred 300 messages.
		fmt.Sprintf(`INSERT INTO mail_message_reads (membership_id, message_id, read_at)
		  SELECT 100, g, now() FROM generate_series(1, %d) AS g WHERE g %% 5 <> 0`, inboxBenchReadable),
		fmt.Sprintf(`INSERT INTO mail_message_stars (membership_id, message_id, starred_at)
		  SELECT 100, g, now() FROM generate_series(1, %d) AS g WHERE g %% 666 = 0`, inboxBenchReadable),
		// VACUUM sets the visibility map autovacuum keeps in production, so index-only scans count.
		`VACUUM ANALYZE`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	inboxScene.repo = NewRepository(db)
	return nil
}

// inboxBenchDay is the bench "today": the window holding the newest inboxBenchToday arrivals.
func inboxBenchDay() (time.Time, time.Time) {
	return inboxScene.now.Add(-(inboxBenchToday - 1) * time.Minute), inboxScene.now.Add(time.Minute)
}

// inboxBenchList does what the service does for one page: read the access rows, list the page,
// then load its snippets.
func inboxBenchList(ctx context.Context, repo *repository, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	if _, err := repo.ListMailboxAccessRows(ctx, 10, inboxBenchMembership); err != nil {
		return nil, err
	}
	query.MailboxIDs, query.MembershipID, query.Limit = inboxBenchMailboxes, inboxBenchMembership, inboxBenchPageSize+1
	messages, err := repo.ListInbox(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(messages) > inboxBenchPageSize {
		messages = messages[:inboxBenchPageSize]
	}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	_, err = repo.MessageSnippets(ctx, ids)
	return messages, err
}

// inboxBenchCounts does what the service does for the counts: access rows, unread per readable
// mailbox, then the view counts.
func inboxBenchCounts(ctx context.Context, repo *repository) (domain.InboxViewCounts, error) {
	if _, err := repo.ListMailboxAccessRows(ctx, 10, inboxBenchMembership); err != nil {
		return domain.InboxViewCounts{}, err
	}
	if _, err := repo.CountUnreadByMailboxIDs(ctx, 10, inboxBenchMembership, inboxBenchMailboxes); err != nil {
		return domain.InboxViewCounts{}, err
	}
	dayStart, dayEnd := inboxBenchDay()
	return repo.CountInboxViews(ctx, inboxBenchMembership, inboxBenchMailboxes, dayStart, dayEnd)
}

// benchInboxPage measures one full page of query. Targets come from design §14.3.
func benchInboxPage(b *testing.B, query domain.InboxQuery, wantFull bool) {
	repo := openInboxScene(b)
	ctx := context.Background()
	measure(b, func() error {
		messages, err := inboxBenchList(ctx, repo, query)
		if err == nil && wantFull && len(messages) != inboxBenchPageSize {
			return fmt.Errorf("page has %d messages, want %d", len(messages), inboxBenchPageSize)
		}
		if err == nil && len(messages) == 0 {
			return fmt.Errorf("empty page")
		}
		return err
	})
}

// BenchmarkInboxAllThreadsFirstPage: view=all&group=thread, first page. Target p95 < 200 ms.
func BenchmarkInboxAllThreadsFirstPage(b *testing.B) {
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewAll, GroupThreads: true}, true)
}

// BenchmarkInboxAllThreadsPage20: view=all&group=thread, 20th page by cursor. Target p95 < 200 ms.
func BenchmarkInboxAllThreadsPage20(b *testing.B) {
	repo := openInboxScene(b)
	ctx := context.Background()
	query := domain.InboxQuery{View: domain.InboxViewAll, GroupThreads: true}
	for page := 1; page < 20; page++ {
		messages, err := inboxBenchList(ctx, repo, query)
		if err != nil || len(messages) != inboxBenchPageSize {
			b.Fatalf("page %d: %d messages, %v", page, len(messages), err)
		}
		last := messages[len(messages)-1]
		query.After = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	benchInboxPage(b, query, true)
}

// BenchmarkInboxToday: view=today, first page. Target p95 < 200 ms.
func BenchmarkInboxToday(b *testing.B) {
	openInboxScene(b)
	dayStart, dayEnd := inboxBenchDay()
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewToday, DayStart: dayStart, DayEnd: dayEnd}, true)
}

// BenchmarkInboxPending: view=pending, first page. Target p95 < 200 ms.
func BenchmarkInboxPending(b *testing.B) {
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewPending}, true)
}

// BenchmarkInboxSent: view=sent&group=thread, first page. Target p95 < 200 ms.
func BenchmarkInboxSent(b *testing.B) {
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewSent, GroupThreads: true}, true)
}

// BenchmarkInboxUnread: view=all&unread=true, ungrouped (D25). Target p95 < 300 ms.
func BenchmarkInboxUnread(b *testing.B) {
	unread := true
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewAll, Unread: &unread}, true)
}

// BenchmarkInboxSearch: a three-character search across the readable mailboxes. Target p95 < 500 ms.
func BenchmarkInboxSearch(b *testing.B) {
	benchInboxPage(b, domain.InboxQuery{View: domain.InboxViewAll, Query: "c4f"}, false)
}

// BenchmarkInboxCounts: the sidebar counts. Target p95 < 300 ms.
func BenchmarkInboxCounts(b *testing.B) {
	repo := openInboxScene(b)
	ctx := context.Background()
	measure(b, func() error {
		counts, err := inboxBenchCounts(ctx, repo)
		if err == nil && counts.Today == 0 {
			return fmt.Errorf("no mail today")
		}
		return err
	})
}

// BenchmarkInboxCountsUnreadableGrowth grows the unreadable mailbox from 50,000 to 500,000
// messages, then measures the counts again. Target: p95 within 10% of BenchmarkInboxCounts.
// It runs last in this file and grows the scene once per binary.
func BenchmarkInboxCountsUnreadableGrowth(b *testing.B) {
	growInboxScene(b)
	BenchmarkInboxCounts(b)
}

// growInboxScene adds inboxBenchGrowth messages to the unreadable mailbox, once per binary.
func growInboxScene(b *testing.B) {
	b.Helper()
	openInboxScene(b)
	inboxScene.grow.Do(func() {
		first := inboxBenchReadable + inboxBenchUnreadable + 1
		for _, statement := range []string{
			inboxMessagesSQL(first, first+inboxBenchGrowth-1, fmt.Sprint(inboxBenchHidden), inboxScene.now),
			`SELECT setval(pg_get_serial_sequence('mail_messages', 'id'), (SELECT max(id) FROM mail_messages))`,
			`VACUUM ANALYZE`,
		} {
			if err := inboxScene.db.Exec(statement).Error; err != nil {
				inboxScene.err = fmt.Errorf("grow: %w", err)
				return
			}
		}
	})
	if inboxScene.err != nil {
		b.Fatal(inboxScene.err)
	}
}
