package testing

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQLRecorder keeps every statement a recorded session runs, with its arguments inlined, so a
// benchmark can EXPLAIN exactly what a repository call sent. It records from GORM callbacks rather
// than the logger, because the database package's params filter strips arguments from log lines.
type SQLRecorder struct {
	mu         sync.Mutex
	statements []string
}

// Record returns a new GORM handle on db's connection pool that records into a new recorder. The
// handle has its own callbacks, so db itself is unchanged.
func Record(db *gorm.DB) (*gorm.DB, *SQLRecorder, error) {
	pool, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	recorded, err := gorm.Open(postgres.New(postgres.Config{Conn: pool, PreferSimpleProtocol: true}),
		&gorm.Config{Logger: logger.Discard})
	if err != nil {
		return nil, nil, err
	}
	recorder := &SQLRecorder{}
	record := func(tx *gorm.DB) {
		if tx.Statement.SQL.Len() == 0 {
			return
		}
		statement := tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...)
		recorder.mu.Lock()
		recorder.statements = append(recorder.statements, statement)
		recorder.mu.Unlock()
	}
	callbacks := recorded.Callback()
	for _, register := range []func() error{
		func() error { return callbacks.Query().After("gorm:query").Register("fastbuild:record", record) },
		func() error { return callbacks.Row().After("gorm:row").Register("fastbuild:record", record) },
		func() error { return callbacks.Raw().After("gorm:raw").Register("fastbuild:record", record) },
		func() error { return callbacks.Create().After("gorm:create").Register("fastbuild:record", record) },
		func() error { return callbacks.Update().After("gorm:update").Register("fastbuild:record", record) },
		func() error { return callbacks.Delete().After("gorm:delete").Register("fastbuild:record", record) },
	} {
		if err := register(); err != nil {
			return nil, nil, err
		}
	}
	return recorded, recorder, nil
}

// Statements returns the recorded statements in order.
func (r *SQLRecorder) Statements() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.statements...)
}

// QueryPlan is one statement with its EXPLAIN (ANALYZE, BUFFERS) output.
type QueryPlan struct {
	SQL  string
	Plan string
}

// SeqScans returns the tables the plan reads with a sequential scan.
func (p QueryPlan) SeqScans() []string {
	var tables []string
	for _, match := range seqScan.FindAllStringSubmatch(p.Plan, -1) {
		tables = append(tables, match[1])
	}
	return tables
}

// Indexes returns the indexes the plan reads.
func (p QueryPlan) Indexes() []string {
	var indexes []string
	for _, match := range indexScan.FindAllStringSubmatch(p.Plan, -1) {
		indexes = append(indexes, match[1])
	}
	return indexes
}

var (
	seqScan   = regexp.MustCompile(`Seq Scan on (\w+)`)
	indexScan = regexp.MustCompile(`(?:Index (?:Only )?Scan(?: Backward)? using|Bitmap Index Scan on) (\w+)`)
)

// errRolledBack ends the EXPLAIN transaction, so an analyzed write never commits.
var errRolledBack = errors.New("explain rolled back")

// ExplainAnalyze runs EXPLAIN (ANALYZE, BUFFERS) for each statement in its own transaction and
// rolls it back, so writes are measured but never kept. Statements that are not SELECT, INSERT,
// UPDATE, or DELETE (BEGIN, SAVEPOINT, ...) are skipped.
func ExplainAnalyze(db *gorm.DB, statements []string) ([]QueryPlan, error) {
	plans := make([]QueryPlan, 0, len(statements))
	for _, statement := range statements {
		head := strings.ToUpper(strings.Fields(strings.TrimSpace(statement) + " x")[0])
		switch head {
		case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH":
		default:
			continue
		}
		var lines []string
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Raw("EXPLAIN (ANALYZE, BUFFERS) " + statement).Scan(&lines).Error; err != nil {
				return err
			}
			return errRolledBack
		})
		if err != nil && !errors.Is(err, errRolledBack) {
			return nil, fmt.Errorf("explain %q: %w", statement, err)
		}
		plans = append(plans, QueryPlan{SQL: statement, Plan: strings.Join(lines, "\n")})
	}
	return plans, nil
}
