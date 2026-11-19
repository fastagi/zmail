package mailbox

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
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
)

// benchMessages is the size of the one shared mailbox the listing benchmarks read.
const benchMessages = 200_000

const (
	benchMailbox    = 1
	benchMembership = 100
)

// benchScene is a schema migrated by the real migrations, so it has every production index, and
// seeded once per test binary; TestMain drops it.
var benchScene struct {
	once   sync.Once
	admin  string
	schema string
	db     *gorm.DB
	repo   *repository
	err    error
}

func TestMain(m *testing.M) {
	code := m.Run()
	dropBenchScene()
	os.Exit(code)
}

func dropBenchScene() {
	if benchScene.db == nil {
		return
	}
	if pool, err := benchScene.db.DB(); err == nil {
		_ = pool.Close()
	}
	admin, err := gorm.Open(postgres.Open(benchScene.admin), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return
	}
	admin.Exec(`DROP SCHEMA IF EXISTS "` + benchScene.schema + `" CASCADE`)
	if pool, err := admin.DB(); err == nil {
		_ = pool.Close()
	}
}

func openBenchScene(b *testing.B) *repository {
	b.Helper()
	dsn := strings.TrimSpace(os.Getenv(testplatform.TestPostgresDSNEnv))
	if dsn == "" {
		b.Skip(testplatform.TestPostgresDSNEnv + " is not set")
	}
	benchScene.once.Do(func() {
		started := time.Now()
		benchScene.err = seedBenchScene(dsn)
		b.Logf("seeded %d messages in %s", benchMessages, time.Since(started).Round(time.Millisecond))
	})
	if benchScene.err != nil {
		b.Fatalf("seed the benchmark mailbox: %v", benchScene.err)
	}
	return benchScene.repo
}

func seedBenchScene(dsn string) error {
	quiet := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), quiet)
	if err != nil {
		return err
	}
	benchScene.admin = dsn
	benchScene.schema = fmt.Sprintf("bench_mail_listing_%d", os.Getpid())
	err = admin.Exec(`CREATE SCHEMA "` + benchScene.schema + `"`).Error
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
	query.Set("search_path", benchScene.schema)
	query.Set("timezone", "UTC")
	scoped.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: scoped.String(), PreferSimpleProtocol: true}), quiet)
	if err != nil {
		return err
	}
	benchScene.db = db

	migrator := migration.NewMigrator(migration.NewDatabaseRepository(db, "migrations"), db, nil)
	migrator.RegisterMany(migrations.All())
	if _, err := migrator.Run(migration.MigratorOptions{}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	statements := []string{
		`INSERT INTO users (id, username, email, password, created_at, updated_at) VALUES (901, 'bench', 'bench@example.com', 'x', now(), now())`,
		`INSERT INTO organizations (id, name, slug, created_by, created_at, updated_at) VALUES (10, 'Bench', 'bench', 901, now(), now())`,
		`INSERT INTO organization_memberships (id, organization_id, user_id, role, created_at, updated_at) VALUES (100, 10, 901, 'owner', now(), now())`,
		`INSERT INTO mailboxes (id, organization_id, created_by, address, created_at, updated_at) VALUES (1, 10, 901, 'team@mail.example', now(), now())`,
		`INSERT INTO mail_folders (id, mailbox_id, name, created_at, updated_at) VALUES (1, 1, 'Clients', now(), now())`,
		`INSERT INTO mail_labels (id, mailbox_id, name, color, created_at, updated_at) VALUES (1, 1, 'Urgent', 'red', now(), now())`,
		// Threads of three; 70% inbound; 5% trash, 5% in a folder, 10% archived, the rest default.
		fmt.Sprintf(`INSERT INTO mail_messages (id, mailbox_id, direction, status, from_address, from_name, to_addresses,
		  subject, text_body, placement, folder_id, trashed_at, thread_id, rfc_message_id, attachment_count, created_at, updated_at)
		SELECT g, 1,
		  CASE WHEN g %% 10 < 7 THEN 'inbound' ELSE 'outbound' END,
		  CASE WHEN g %% 10 < 7 THEN 'received' ELSE 'sent' END,
		  'sender' || (g %% 5000) || '@example.com', 'Sender ' || (g %% 5000), '["team@mail.example"]',
		  'Order ' || md5(g::text),
		  'Hello, this is message ' || g || ' about ' || md5((g * 7)::text) || ' and ' || md5((g * 13)::text),
		  CASE WHEN g %% 20 = 0 THEN 'trash' WHEN g %% 20 = 1 THEN 'folder' WHEN g %% 10 = 2 THEN 'archive' ELSE 'default' END,
		  CASE WHEN g %% 20 = 1 THEN 1 END,
		  CASE WHEN g %% 20 = 0 THEN now() END,
		  ((g - 1) / 3) * 3 + 1,
		  'm' || g || '@example.com',
		  CASE WHEN g %% 15 = 0 THEN 1 ELSE 0 END,
		  now() - (%d - g) * interval '1 minute', now() - (%d - g) * interval '1 minute'
		FROM generate_series(1, %d) AS g`, benchMessages, benchMessages, benchMessages),
		`SELECT setval(pg_get_serial_sequence('mail_messages', 'id'), (SELECT max(id) FROM mail_messages))`,
		fmt.Sprintf(`INSERT INTO mail_message_reads (membership_id, message_id, read_at)
		  SELECT 100, g, now() FROM generate_series(1, %d) AS g WHERE g %% 10 < 7 AND g %% 2 = 0`, benchMessages),
		fmt.Sprintf(`INSERT INTO mail_message_stars (membership_id, message_id, starred_at)
		  SELECT 100, g, now() FROM generate_series(1, %d) AS g WHERE g %% 50 = 0`, benchMessages),
		fmt.Sprintf(`INSERT INTO mail_message_labels (message_id, label_id)
		  SELECT g, 1 FROM generate_series(1, %d) AS g WHERE g %% 25 = 0`, benchMessages),
		`ANALYZE`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	benchScene.repo = NewRepository(db)
	return nil
}

// measure runs fn b.N times and reports the 50th and 95th percentile in milliseconds.
func measure(b *testing.B, fn func() error) {
	b.Helper()
	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		started := time.Now()
		if err := fn(); err != nil {
			b.Fatal(err)
		}
		durations = append(durations, time.Since(started))
	}
	b.StopTimer()
	slices.Sort(durations)
	percentile := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(durations)))) - 1
		return float64(durations[max(index, 0)].Microseconds()) / 1000
	}
	b.ReportMetric(percentile(0.50), "p50-ms")
	b.ReportMetric(percentile(0.95), "p95-ms")
}

// BenchmarkThreadedListing200k loads the first page of the Inbox grouped by thread, as the web
// client does, in a shared mailbox of 200,000 messages. Target: p95 < 200 ms.
func BenchmarkThreadedListing200k(b *testing.B) {
	repo := openBenchScene(b)
	ctx := context.Background()
	for _, view := range []struct {
		name   string
		filter domain.MailMessageFilter
	}{
		{"inbox", domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: domain.MailDirectionInbound}},
		{"sent", domain.MailMessageFilter{Placement: domain.MailPlacementDefault, Direction: domain.MailDirectionOutbound}},
		{"archive", domain.MailMessageFilter{Placement: domain.MailPlacementArchive}},
	} {
		b.Run(view.name, func(b *testing.B) {
			filter := view.filter
			filter.GroupThreads, filter.Page, filter.PerPage = true, 1, 50
			measure(b, func() error {
				threads, total, err := repo.ListThreads(ctx, benchMailbox, benchMembership, filter)
				if err == nil && (len(threads) != 50 || total == 0) {
					err = fmt.Errorf("listed %d threads of %d", len(threads), total)
				}
				return err
			})
		})
	}
}

// BenchmarkSearch200k searches the same mailbox with texts of three or more characters.
// Target: p95 < 500 ms.
func BenchmarkSearch200k(b *testing.B) {
	repo := openBenchScene(b)
	ctx := context.Background()
	for _, search := range []struct{ name, query string }{
		// md5('101') starts 38b3eff8; message 101 (in a folder) has it in its subject.
		{"rare", "38b3eff8"},
		// About 2% of the senders: sender42, sender420-429, sender4200-4299.
		{"common", "sender42"},
		{"three-characters", "abc"},
	} {
		b.Run(search.name, func(b *testing.B) {
			filter := domain.MailMessageFilter{Query: search.query, Page: 1, PerPage: 50}
			measure(b, func() error {
				_, total, err := repo.ListMessages(ctx, benchMailbox, benchMembership, filter)
				if err == nil && total == 0 {
					err = fmt.Errorf("search %q found nothing", search.query)
				}
				return err
			})
		})
	}
}
