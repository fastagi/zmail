package mailbox

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/fastagi/zmail/api/internal/modules/organization"
)

// The v2 hot-path scene (docs/benchmarks/v2-hot-paths.md): 500 organizations, 2,000 members, and
// 1,198 mailboxes. Organization 1 is the unified inbox organization: 200 mailboxes, the last 20
// of them external (Gmail and Outlook), AI triage on the first 100, 100,000 messages, and 5,000
// platform accounts. Every mailbox shares 300,000 outbound messages over 60 days, about 5,000 a
// day, with delivery receipts for the last 31 days.
const (
	v2Organizations     = 500
	v2Users             = 2_000
	v2InboxMailboxes    = 200
	v2ExternalFrom      = 181
	v2TriageMailboxes   = 100
	v2Mailboxes         = v2InboxMailboxes + 2*(v2Organizations-1)
	v2InboxMessages     = 100_000
	v2OutboundMessages  = 300_000
	v2HistoryMinutes    = 60 * 24 * 60
	v2PlatformAccounts  = 15_000
	v2InboxAccounts     = 5_000
	v2InboxOrganization = 1
	v2InboxMembership   = 1
	v2PageSize          = 20
)

var v2Scene struct {
	once   sync.Once
	admin  string
	schema string
	db     *gorm.DB
	repo   *repository
	now    time.Time
	err    error
	ids    []uint

	explained sync.Map
}

func dropV2Scene() {
	if v2Scene.db == nil {
		return
	}
	if pool, err := v2Scene.db.DB(); err == nil {
		_ = pool.Close()
	}
	admin, err := gorm.Open(postgres.Open(v2Scene.admin), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return
	}
	admin.Exec(`DROP SCHEMA IF EXISTS "` + v2Scene.schema + `" CASCADE`)
	if pool, err := admin.DB(); err == nil {
		_ = pool.Close()
	}
}

func openV2Scene(b *testing.B) *repository {
	b.Helper()
	dsn := strings.TrimSpace(os.Getenv(testplatform.TestPostgresDSNEnv))
	if dsn == "" {
		b.Skip(testplatform.TestPostgresDSNEnv + " is not set")
	}
	v2Scene.once.Do(func() {
		started := time.Now()
		v2Scene.err = seedV2Scene(dsn)
		b.Logf("seeded the v2 scene in %s", time.Since(started).Round(time.Millisecond))
	})
	if v2Scene.err != nil {
		b.Fatalf("seed the v2 scene: %v", v2Scene.err)
	}
	return v2Scene.repo
}

func seedV2Scene(dsn string) error {
	quiet := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), quiet)
	if err != nil {
		return err
	}
	v2Scene.admin = dsn
	v2Scene.schema = fmt.Sprintf("bench_mail_v2_%d", os.Getpid())
	err = admin.Exec(`CREATE SCHEMA "` + v2Scene.schema + `"`).Error
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
	query.Set("search_path", v2Scene.schema)
	query.Set("timezone", "UTC")
	scoped.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: scoped.String(), PreferSimpleProtocol: true}), quiet)
	if err != nil {
		return err
	}
	v2Scene.db = db
	v2Scene.now = time.Now().UTC().Truncate(time.Minute)

	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	if _, err := migrator.Run(migration.MigratorOptions{}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	now := v2Scene.now.Format(time.RFC3339Nano)
	// arrival spreads message g over the last 60 days, a permutation of one-minute steps.
	arrival := fmt.Sprintf(`'%s'::timestamptz - ((g::bigint * 7919) %% %d) * interval '1 minute'`, now, v2HistoryMinutes)
	statements := []string{
		// The seeded default users would collide with the bench ids.
		`DELETE FROM users`,
		// Every 40th user is disabled.
		fmt.Sprintf(`INSERT INTO users (id, username, email, password, status, created_at, updated_at)
		  SELECT n, 'user' || n, 'user' || n || '@example.com', 'x', CASE WHEN n %% 40 = 0 THEN 2 ELSE 1 END, now(), now()
		  FROM generate_series(1, %d) AS n`, v2Users),
		fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('users', 'id'), %d)`, v2Users),
		// Every tenth organization is an "Acme", the name search target.
		fmt.Sprintf(`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at)
		  SELECT n, CASE WHEN n %% 10 = 0 THEN 'Acme Works ' || n ELSE 'Company ' || n END, 'org-' || n, n, now(), now()
		  FROM generate_series(1, %d) AS n`, v2Organizations),
		fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('organizations', 'id'), %d)`, v2Organizations),
		// Four members per organization; user 1 is the bench reader of organization 1.
		fmt.Sprintf(`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at)
		  SELECT n, ((n - 1) %% %d) + 1, n, CASE WHEN n BETWEEN %d AND %d THEN 'owner' ELSE 'member' END, now(), now()
		  FROM generate_series(1, %d) AS n`, v2Organizations, v2Organizations+1, 2*v2Organizations, v2Users),
		fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('organization_memberships', 'id'), %d)`, v2Users),
		fmt.Sprintf(`INSERT INTO mailboxes (id, organization_id, created_by, address, type, origin, owner_user_id, ai_triage_enabled, created_at, updated_at)
		  SELECT n, 1, 1, 'box' || n || '@mail.example',
		    CASE WHEN n >= %[1]d THEN 'personal' ELSE 'shared' END, CASE WHEN n >= %[1]d THEN 'connected' ELSE 'zmail' END,
		    CASE WHEN n >= %[1]d THEN 1 END,
		    n <= %[2]d, now(), now()
		  FROM generate_series(1, %[3]d) AS n`, v2ExternalFrom, v2TriageMailboxes, v2InboxMailboxes),
		fmt.Sprintf(`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at)
		  SELECT n, 2 + (n - %[1]d) / 2, 1, 'box' || n || '@mail.example', now(), now()
		  FROM generate_series(%[1]d, %[2]d) AS n`, v2InboxMailboxes+1, v2Mailboxes),
		fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('mailboxes', 'id'), %d)`, v2Mailboxes),
		fmt.Sprintf(`INSERT INTO mailbox_members (mailbox_id, membership_id, user_id, permission, granted_by, created_at, updated_at)
		  SELECT n, 1, 1, 'view', 1, now(), now() FROM generate_series(1, %d) AS n`, v2InboxMailboxes),
		`INSERT INTO mail_triage_settings (organization_id, enabled, created_at, updated_at) VALUES (1, true, now(), now())`,
		// Organization 1: 500 messages per mailbox, threads of three, 85% inbound, 5% trash, 10%
		// archived. External mailboxes carry Gmail (181-195) and Outlook (196-200) copies.
		fmt.Sprintf(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, from_name, to_addresses,
		  subject, text_body, placement, trashed_at, thread_id, rfc_message_id, source, external_id, created_at, updated_at)
		SELECT g, b.box,
		  CASE WHEN g %% 20 < 17 THEN 'inbound' ELSE 'outbound' END,
		  CASE WHEN g %% 20 < 17 THEN 'received' ELSE 'sent' END,
		  'sender' || (g %% 5000) || '@example.com', 'Sender ' || (g %% 5000), '["team@mail.example"]',
		  'Order ' || md5(g::text), 'Hello, this is message ' || g || ' about ' || md5((g * 7)::text),
		  CASE WHEN g %% 20 = 0 THEN 'trash' WHEN g %% 10 = 2 THEN 'archive' ELSE 'default' END,
		  CASE WHEN g %% 20 = 0 THEN '%[3]s'::timestamptz END,
		  ((g - 1) / 3) * 3 + 1, 'm' || g || '@example.com',
		  CASE WHEN b.box >= 196 THEN 'outlook' WHEN b.box >= %[4]d THEN 'gmail' ELSE 'zmail' END,
		  CASE WHEN b.box >= %[4]d THEN 'x' || g END,
		  %[5]s, %[5]s
		FROM generate_series(1, %[1]d) AS g, LATERAL (SELECT 1 + (g - 1) / %[2]d AS box) AS b`,
			v2InboxMessages, v2InboxMessages/v2InboxMailboxes, now, v2ExternalFrom, arrival),
		// The platform outbound log: every mailbox, 92% sent, 5% failed, 3% queued. Outbound
		// copies in external mailboxes carry their provider as source and are not counted.
		fmt.Sprintf(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, to_addresses,
		  subject, text_body, placement, thread_id, rfc_message_id, source, external_id, created_at, updated_at, send_attempted_at)
		SELECT g, b.box, 'outbound',
		  CASE WHEN g %% 100 < 92 THEN 'sent' WHEN g %% 100 < 97 THEN 'failed' ELSE 'queued' END,
		  'box' || b.box || '@mail.example', '["customer' || (g %% 9000) || '@example.net"]',
		  'Invoice ' || g, 'Outbound message ' || g, 'default', g, 'o' || g || '@mail.example',
		  CASE WHEN b.box BETWEEN 196 AND %[4]d THEN 'outlook' WHEN b.box BETWEEN %[3]d AND %[4]d THEN 'gmail' ELSE 'zmail' END,
		  CASE WHEN b.box BETWEEN %[3]d AND %[4]d THEN 'x' || g END,
		  %[5]s, %[5]s, %[5]s
		FROM generate_series(%[1]d, %[2]d) AS g, LATERAL (SELECT 1 + (g %% %[6]d) AS box) AS b`,
			v2InboxMessages+1, v2InboxMessages+v2OutboundMessages, v2ExternalFrom, v2InboxMailboxes, arrival, v2Mailboxes),
		`SELECT setval(pg_get_serial_sequence('mail_messages', 'id'), (SELECT max(id) FROM mail_messages))`,
		// Triage results for the inbound mail of the first 100 mailboxes: 15% reply, 5% deadline,
		// 60% FYI, 20% review. The other mailboxes have triage off and no rows.
		fmt.Sprintf(`INSERT INTO mail_triage (message_id, mailbox_id, category, source, status, next_attempt_at, created_at, updated_at)
		  SELECT id, mailbox_id,
		    CASE WHEN (id / 20) %% 20 < 3 THEN 'reply' WHEN (id / 20) %% 20 = 3 THEN 'deadline'
		         WHEN (id / 20) %% 20 < 16 THEN 'fyi' ELSE 'review' END,
		    'ai', 'done', created_at, created_at, created_at
		  FROM mail_messages WHERE direction = 'inbound' AND mailbox_id <= %d`, v2TriageMailboxes),
		// The bench reader has read 80% of organization 1's mail.
		fmt.Sprintf(`INSERT INTO mail_message_reads (membership_id, message_id, read_at)
		  SELECT 1, g, now() FROM generate_series(1, %d) AS g WHERE g %% 5 <> 0`, v2InboxMessages),
		// Receipts for the sent mail of the last 31 days: 1% bounced, 0.1% complained.
		fmt.Sprintf(`INSERT INTO mail_message_events (message_id, type, occurred_at, created_at)
		  SELECT id, CASE WHEN id %% 100 = 1 THEN 'bounced' WHEN id %% 1000 = 2 THEN 'complained' ELSE 'delivered' END,
		    created_at + interval '1 minute', now()
		  FROM mail_messages
		  WHERE id > %d AND status = 'sent' AND created_at >= '%s'::timestamptz - interval '31 days'`, v2InboxMessages, now),
		// Platform accounts: 5,000 in organization 1, 21 in each of the others.
		fmt.Sprintf(`INSERT INTO platform_accounts (id, organization_id, platform, handle, mailbox_id, owner_user_id, status, created_at, updated_at)
		  SELECT g, o.org,
		    (ARRAY['github','x','linkedin','reddit','youtube','discord','producthunt','medium','tiktok','instagram'])[1 + g %% 10],
		    'acct' || g,
		    CASE WHEN o.org = 1 THEN 1 + g %% %[3]d ELSE %[4]d + 2 * (o.org - 2) END,
		    CASE WHEN o.org = 1 THEN 1 + 500 * (g %% 4) ELSE o.org END,
		    CASE WHEN (g / 7) %% 10 < 6 THEN 'active' WHEN (g / 7) %% 10 = 6 THEN 'invited'
		         WHEN (g / 7) %% 10 = 7 THEN 'awaiting_confirmation' WHEN (g / 7) %% 10 = 8 THEN 'needs_review' ELSE 'retired' END,
		    now(), now()
		  FROM generate_series(1, %[1]d) AS g,
		    LATERAL (SELECT CASE WHEN g <= %[2]d THEN 1 ELSE 2 + (g - %[2]d - 1) / 21 END AS org) AS o`,
			v2PlatformAccounts, v2InboxAccounts, v2ExternalFrom-1, v2InboxMailboxes+1),
		fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('platform_accounts', 'id'), %d)`, v2PlatformAccounts),
		`VACUUM ANALYZE`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	v2Scene.repo = NewRepository(db)
	v2Scene.ids = make([]uint, v2InboxMailboxes)
	for i := range v2Scene.ids {
		v2Scene.ids[i] = uint(i + 1)
	}
	return nil
}

// explainV2Once runs fn once against a recording repository and organization directory, then
// EXPLAIN (ANALYZE, BUFFERS)es every statement it sent, once per benchmark name. It logs one
// summary line per statement, writes the full plans to $ZMAIL_BENCH_EXPLAIN_DIR/<benchmark>.txt
// when that is set, and fails the benchmark when a statement sequentially scans a forbidden table.
func explainV2Once(b *testing.B, forbidden []string, fn func(*repository, domain.PlatformOrganizationDirectory) error) {
	b.Helper()
	if _, done := v2Scene.explained.LoadOrStore(b.Name(), true); done {
		return
	}
	recorded, recorder, err := testplatform.Record(v2Scene.db)
	if err != nil {
		b.Fatal(err)
	}
	if runErr := fn(NewRepository(recorded), organization.NewRepository(recorded)); runErr != nil {
		b.Fatalf("explain run: %v", runErr)
	}
	plans, err := testplatform.ExplainAnalyze(v2Scene.db, recorder.Statements())
	if err != nil {
		b.Fatal(err)
	}
	var full strings.Builder
	for _, plan := range plans {
		elapsed := ""
		for line := range strings.Lines(plan.Plan) {
			if strings.HasPrefix(line, "Execution Time:") {
				elapsed = strings.TrimSpace(strings.TrimPrefix(line, "Execution Time:"))
			}
		}
		sql := strings.Join(strings.Fields(plan.SQL), " ")
		if len(sql) > 120 {
			sql = sql[:120] + "..."
		}
		b.Logf("explain %s seq=%v idx=%v :: %s", elapsed, plan.SeqScans(), slices.Compact(slices.Sorted(slices.Values(plan.Indexes()))), sql)
		fmt.Fprintf(&full, "%s\n\n%s\n\n", plan.SQL, plan.Plan)
		for _, table := range plan.SeqScans() {
			if slices.Contains(forbidden, table) {
				b.Errorf("sequential scan on %s: %s", table, sql)
			}
		}
	}
	if dir := os.Getenv("ZMAIL_BENCH_EXPLAIN_DIR"); dir != "" {
		name := strings.NewReplacer("/", "_", "=", "-", "+", "-").Replace(b.Name()) + ".txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(full.String()), 0o600); err != nil {
			b.Fatal(err)
		}
	}
}

// v2InboxList does what the service does for one page: access rows, the triage switch, the page,
// its snippets, and its triage results.
func v2InboxList(ctx context.Context, repo *repository, query domain.InboxQuery) ([]*domain.MailMessage, error) {
	if _, err := repo.ListMailboxAccessRows(ctx, v2InboxOrganization, v2InboxMembership); err != nil {
		return nil, err
	}
	if _, err := repo.FindTriageSettings(ctx, v2InboxOrganization); err != nil {
		return nil, err
	}
	query.MailboxIDs, query.MembershipID, query.Limit = v2Scene.ids, v2InboxMembership, v2PageSize+1
	messages, err := repo.ListInbox(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(messages) > v2PageSize {
		messages = messages[:v2PageSize]
	}
	ids := make([]uint, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.ID)
	}
	if _, err = repo.MessageSnippets(ctx, ids); err != nil {
		return nil, err
	}
	_, err = repo.ListTriage(ctx, ids)
	return messages, err
}

var v2LargeTables = []string{"mail_messages", "mail_triage", "mail_message_reads", "mail_message_events"}

// BenchmarkV2InboxTriage lists the first unified inbox page of 200 mailboxes filtered by an AI
// triage category. Half the mailboxes have triage off and no triage rows. Target p95 < 200 ms.
func BenchmarkV2InboxTriage(b *testing.B) {
	repo := openV2Scene(b)
	ctx := context.Background()
	for _, filter := range []struct {
		name       string
		categories []domain.MailTriageCategory
	}{
		{"reply", []domain.MailTriageCategory{domain.MailTriageReply}},
		{"deadline", []domain.MailTriageCategory{domain.MailTriageDeadline}},
		{"reply+review", []domain.MailTriageCategory{domain.MailTriageReply, domain.MailTriageReview}},
	} {
		b.Run(filter.name, func(b *testing.B) {
			query := domain.InboxQuery{View: domain.InboxViewAll, Triage: filter.categories}
			list := func(repo *repository) error {
				messages, err := v2InboxList(ctx, repo, query)
				if err == nil && len(messages) != v2PageSize {
					err = fmt.Errorf("page has %d messages, want %d", len(messages), v2PageSize)
				}
				return err
			}
			explainV2Once(b, v2LargeTables, func(repo *repository, _ domain.PlatformOrganizationDirectory) error { return list(repo) })
			measure(b, func() error { return list(repo) })
		})
	}
}

// BenchmarkV2InboxTriageCounts counts the reader's unread Inbox mail per triage category across the
// 200 mailboxes. Target p95 < 300 ms.
func BenchmarkV2InboxTriageCounts(b *testing.B) {
	repo := openV2Scene(b)
	ctx := context.Background()
	count := func(repo *repository) error {
		if _, err := repo.FindTriageSettings(ctx, v2InboxOrganization); err != nil {
			return err
		}
		counts, err := repo.CountTriage(ctx, v2InboxMembership, v2Scene.ids)
		if err == nil && counts.Reply == 0 {
			err = fmt.Errorf("no reply mail counted")
		}
		return err
	}
	explainV2Once(b, nil, func(repo *repository, _ domain.PlatformOrganizationDirectory) error { return count(repo) })
	measure(b, func() error { return count(repo) })
}

// BenchmarkV2InboxSources lists unified inbox pages across 200 mailboxes, 20 of them external, so
// pages mix zmail, Gmail, and Outlook rows. Target p95 < 200 ms.
func BenchmarkV2InboxSources(b *testing.B) {
	repo := openV2Scene(b)
	ctx := context.Background()
	for _, view := range []struct {
		name  string
		query domain.InboxQuery
	}{
		{"all", domain.InboxQuery{View: domain.InboxViewAll}},
		{"all-threads", domain.InboxQuery{View: domain.InboxViewAll, GroupThreads: true}},
	} {
		b.Run(view.name, func(b *testing.B) {
			list := func(repo *repository) error {
				messages, err := v2InboxList(ctx, repo, view.query)
				if err == nil && len(messages) != v2PageSize {
					err = fmt.Errorf("page has %d messages, want %d", len(messages), v2PageSize)
				}
				return err
			}
			explainV2Once(b, v2LargeTables, func(repo *repository, _ domain.PlatformOrganizationDirectory) error { return list(repo) })
			measure(b, func() error { return list(repo) })
		})
	}
	// The scene must actually serve external rows through the listing.
	sources := map[domain.MailSource]int{}
	query := domain.InboxQuery{View: domain.InboxViewAll}
	for range 10 {
		messages, err := v2InboxList(ctx, repo, query)
		if err != nil {
			b.Fatal(err)
		}
		for _, message := range messages {
			sources[message.Source]++
		}
		last := messages[len(messages)-1]
		query.After = &domain.InboxCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if sources[domain.MailSourceZmail] == 0 || sources[domain.MailSource("gmail")] == 0 {
		b.Fatalf("the first ten pages lack a source: %v", sources)
	}
}

// v2Metrics is the operator metrics handler over the scene, with the real organization directory.
func v2Metrics(repo *repository, directory domain.PlatformOrganizationDirectory) *OperatorMetricsHandler {
	return &OperatorMetricsHandler{store: repo, organizationsDirectory: directory, now: func() time.Time { return v2Scene.now }}
}

func v2Today() time.Time {
	now := v2Scene.now
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// benchV2Metrics explains run once and then measures it.
func benchV2Metrics(b *testing.B, run func(*OperatorMetricsHandler) error) {
	repo := openV2Scene(b)
	explainV2Once(b, []string{"mail_messages", "mail_message_events"}, func(repo *repository, directory domain.PlatformOrganizationDirectory) error {
		return run(v2Metrics(repo, directory))
	})
	handler := v2Metrics(repo, organization.NewRepository(v2Scene.db))
	measure(b, func() error { return run(handler) })
}

// BenchmarkV2OperatorOverview is the operator mail overview for today with a 7- and 31-day trend.
// Target p95 < 300 ms.
func BenchmarkV2OperatorOverview(b *testing.B) {
	for _, days := range []int{7, 31} {
		b.Run(fmt.Sprintf("days=%d", days), func(b *testing.B) {
			benchV2Metrics(b, func(h *OperatorMetricsHandler) error {
				overview, err := h.overview(context.Background(), v2Today(), days)
				if err == nil && (overview.Organizations != v2Organizations || overview.Trend[0].Sent == 0) {
					err = fmt.Errorf("overview %+v", overview)
				}
				return err
			})
		})
	}
}

// BenchmarkV2OperatorOrganizations is one day of per-organization counts, page 1, unfiltered and
// searched by name. Target p95 < 300 ms.
func BenchmarkV2OperatorOrganizations(b *testing.B) {
	for _, search := range []struct{ name, query string }{{"all", ""}, {"name=acme", "acme"}} {
		b.Run(search.name, func(b *testing.B) {
			benchV2Metrics(b, func(h *OperatorMetricsHandler) error {
				rows, total, err := h.organizations(context.Background(), v2Today().AddDate(0, 0, -1), search.query, 1, operatorMetricsPerPage)
				if err == nil && (len(rows) != operatorMetricsPerPage || rows[0].Sent == 0 || total == 0) {
					err = fmt.Errorf("%d rows of %d", len(rows), total)
				}
				return err
			})
		})
	}
}

// BenchmarkV2OperatorOutbound is one day of the outbound log (about 4,900 counted messages):
// page 1, page 100, and page 1 of one organization. Target p95 < 300 ms.
func BenchmarkV2OperatorOutbound(b *testing.B) {
	for _, page := range []struct {
		name   string
		filter outboundFilter
		page   int
	}{
		{"page=1", outboundFilter{}, 1},
		{"page=100", outboundFilter{}, 100},
		{"organization", outboundFilter{OrganizationID: 7}, 1},
	} {
		b.Run(page.name, func(b *testing.B) {
			benchV2Metrics(b, func(h *OperatorMetricsHandler) error {
				filter := page.filter
				filter.Day = v2Today().AddDate(0, 0, -1)
				rows, total, err := h.outbound(context.Background(), filter, page.page, operatorMetricsPerPage)
				if err == nil && (len(rows) == 0 || total == 0) {
					err = fmt.Errorf("%d rows of %d", len(rows), total)
				}
				return err
			})
		})
	}
}

// BenchmarkV2PlatformAccounts lists organization 1's 5,000 platform accounts with search and
// filters, as an admin and as a member who sees ten mailboxes. Target p95 < 100 ms.
func BenchmarkV2PlatformAccounts(b *testing.B) {
	repo := openV2Scene(b)
	ctx := context.Background()
	visible := []uint{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, list := range []struct {
		name   string
		filter domain.PlatformAccountFilter
	}{
		{"all", domain.PlatformAccountFilter{}},
		{"q=handle", domain.PlatformAccountFilter{Query: "acct12"}},
		{"q=address", domain.PlatformAccountFilter{Query: "box17@"}},
		{"status+mailbox", domain.PlatformAccountFilter{Status: domain.PlatformAccountStatusActive, MailboxID: 17}},
		{"member+q", domain.PlatformAccountFilter{Query: "acct1", Visible: visible, VisibleOwner: 1, AddressMailboxIDs: visible}},
	} {
		b.Run(list.name, func(b *testing.B) {
			filter := list.filter
			filter.Page, filter.PerPage = 1, 15
			run := func(repo *repository) error {
				accounts, total, err := repo.ListPlatformAccounts(ctx, v2InboxOrganization, filter)
				if err == nil && (len(accounts) == 0 || total == 0) {
					err = fmt.Errorf("%d accounts of %d", len(accounts), total)
				}
				return err
			}
			explainV2Once(b, nil, func(repo *repository, _ domain.PlatformOrganizationDirectory) error { return run(repo) })
			measure(b, func() error { return run(repo) })
		})
	}
}
