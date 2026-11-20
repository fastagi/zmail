package user

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/fastagi/zmail/api/database/migrations"
	testplatform "github.com/fastagi/zmail/api/internal/infra/testing"
)

// registrationCodeBenchRows is the stored code history: 100,000 addresses with two codes each,
// half of them older than the 24-hour retention (docs/benchmarks/v2-hot-paths.md).
const registrationCodeBenchRows = 200_000

// registrationCodeScene is one benchmark's seeded schema; OpenPostgres drops it when the
// benchmark ends.
type registrationCodeScene struct {
	db   *gorm.DB
	repo *repository
	now  time.Time
}

func openRegistrationCodeScene(b *testing.B) *registrationCodeScene {
	b.Helper()
	db := testplatform.OpenPostgres(b, nil, &UserPO{})
	scene := &registrationCodeScene{db: db, repo: NewRepository(db), now: time.Now().UTC().Truncate(time.Second)}
	if err := migrations.All()[registrationEmailCodeMigration].Up(db); err != nil {
		b.Fatal(err)
	}
	for _, statement := range []string{
		// Address seed<k> holds codes 2k and 2k+1, created within the last 48 hours.
		fmt.Sprintf(`INSERT INTO registration_email_codes (email, code_hash, expires_at, attempts, consumed_at, created_at, updated_at)
		  SELECT 'seed' || (g / 2) || '@example.test', md5(g::text) || md5((g * 7)::text),
		    t.at + interval '10 minutes', g %% 3, CASE WHEN g %% 4 = 0 THEN t.at END, t.at, t.at
		  FROM generate_series(2, %d) AS g,
		    LATERAL (SELECT '%s'::timestamptz - (g %% 2880) * interval '1 minute' AS at) AS t`,
			registrationCodeBenchRows+1, scene.now.Format(time.RFC3339)),
		`VACUUM ANALYZE registration_email_codes`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			b.Fatalf("seed registration codes: %v", err)
		}
	}
	return scene
}

// measureRegistration runs fn b.N times and reports the 50th and 95th percentile in milliseconds.
func measureRegistration(b *testing.B, fn func(i int) error) {
	b.Helper()
	durations := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := range b.N {
		started := time.Now()
		if err := fn(i); err != nil {
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

// explainRegistration runs fn once on a recording handle, EXPLAIN (ANALYZE, BUFFERS)es what it
// sent, logs a summary line per statement, and fails on a sequential scan of the code table.
func explainRegistration(b *testing.B, scene *registrationCodeScene, fn func(*repository) error) {
	b.Helper()
	recorded, recorder, err := testplatform.Record(scene.db)
	if err != nil {
		b.Fatal(err)
	}
	if runErr := fn(NewRepository(recorded)); runErr != nil {
		b.Fatal(runErr)
	}
	plans, err := testplatform.ExplainAnalyze(scene.db, recorder.Statements())
	if err != nil {
		b.Fatal(err)
	}
	for _, plan := range plans {
		sql := strings.Join(strings.Fields(plan.SQL), " ")
		if len(sql) > 110 {
			sql = sql[:110] + "..."
		}
		b.Logf("explain seq=%v idx=%v :: %s", plan.SeqScans(), plan.Indexes(), sql)
		if slices.Contains(plan.SeqScans(), "registration_email_codes") {
			b.Errorf("sequential scan on registration_email_codes:\n%s\n%s", plan.SQL, plan.Plan)
		}
	}
}

// BenchmarkRegistrationEmailCodeIssue issues a code to an address with stored history, over
// 200,000 stored codes: the advisory lock, the per-address window and cooldown count, the
// retention delete, and the insert. Target p95 < 20 ms.
func BenchmarkRegistrationEmailCodeIssue(b *testing.B) {
	scene := openRegistrationCodeScene(b)
	ctx := context.Background()
	issue := func(repo *repository, k int) error {
		email := fmt.Sprintf("seed%d@example.test", 1+k%(registrationCodeBenchRows/2))
		result, err := repo.IssueRegistrationEmailCode(ctx, email, registrationEmailCodeHash(email, "123456"),
			scene.now.Add(2*time.Minute), defaultRegistrationEmailCodePolicy)
		if err == nil && result != registrationEmailCodeIssued {
			err = fmt.Errorf("issue to %s: %s", email, result)
		}
		return err
	}
	explainRegistration(b, scene, func(repo *repository) error { return issue(repo, registrationCodeBenchRows/2-1) })
	measureRegistration(b, func(i int) error { return issue(scene.repo, i) })
}

// BenchmarkRegistrationEmailCodeConsume registers with the right code of an address with stored
// history; every iteration consumes a code issued before the timer starts. Target p95 < 20 ms.
func BenchmarkRegistrationEmailCodeConsume(b *testing.B) {
	scene := openRegistrationCodeScene(b)
	ctx := context.Background()
	emails := make([]string, b.N+1)
	for i := range emails {
		emails[i] = fmt.Sprintf("seed%d@example.test", 1+i%(registrationCodeBenchRows/2))
		if _, err := scene.repo.IssueRegistrationEmailCode(ctx, emails[i], registrationEmailCodeHash(emails[i], "654321"),
			scene.now.Add(2*time.Minute), defaultRegistrationEmailCodePolicy); err != nil {
			b.Fatal(err)
		}
	}
	consume := func(repo *repository, email string) error {
		return repo.RegisterWithEmailCode(ctx, email, registrationEmailCodeHash(email, "654321"),
			scene.now.Add(3*time.Minute), registrationEmailCodeMaxAttempts, func(context.Context) error { return nil })
	}
	explainRegistration(b, scene, func(repo *repository) error { return consume(repo, emails[b.N]) })
	measureRegistration(b, func(i int) error { return consume(scene.repo, emails[i]) })
}
