package testing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A package whose TestMain calls RunWithPostgresPool prepares each distinct schema once and lends
// it to one test at a time. Between tests the pool empties every table, restores the rows the
// preparation wrote (such as the migration ledger), and rewinds the sequences, so each test sees
// exactly what a freshly prepared schema holds. A test that changes the schema's structure gives
// its schema up instead: the pool drops it and prepares a new one for the next test.
//
// Without RunWithPostgresPool every call prepares a fresh schema, as OpenPostgres always has.

var schemaPool struct {
	mu      sync.Mutex
	enabled bool
	free    map[string][]*pooledSchema
	all     []*pooledSchema
}

type pooledSchema struct {
	name        string
	maintenance *gorm.DB
	fingerprint string
	// seeded lists the tables the preparation filled; their rows live in the snapshot schema.
	seeded []string
	// deleteOrder lists every table with referencing tables before the tables they reference.
	deleteOrder []string
	// sequences holds each sequence's prepared position.
	sequences []sequenceState
}

type sequenceState struct {
	Name  string
	Value int64
	// Called is false for a sequence the preparation never advanced; Value is then its start.
	Called bool
}

// RunWithPostgresPool runs the package's tests with pooled schemas and drops them afterwards.
// Call it from TestMain: os.Exit(testplatform.RunWithPostgresPool(m)).
func RunWithPostgresPool(m *testing.M) int {
	schemaPool.mu.Lock()
	schemaPool.enabled = true
	schemaPool.free = map[string][]*pooledSchema{}
	schemaPool.mu.Unlock()
	code := m.Run()
	dropSchemaPool()
	return code
}

// OpenPooledPostgres returns a schema that prepare has set up, reusing a pooled one when the
// package runs with RunWithPostgresPool. key names what prepare builds: calls with the same key
// must prepare the same schema.
func OpenPooledPostgres(tb testing.TB, gormConfig *gorm.Config, key string, prepare func(*gorm.DB) error) *gorm.DB {
	tb.Helper()
	db, release := openPooled(tb, gormConfig, key, prepare)
	tb.Cleanup(release)
	return db
}

func openPooled(tb testing.TB, gormConfig *gorm.Config, key string, prepare func(*gorm.DB) error) (*gorm.DB, func()) {
	tb.Helper()
	schemaPool.mu.Lock()
	enabled := schemaPool.enabled
	schemaPool.mu.Unlock()
	if !enabled {
		db, cleanup := OpenPostgresWithCleanup(tb, gormConfig)
		if err := prepare(db); err != nil {
			cleanup()
			tb.Fatalf("prepare isolated PostgreSQL schema: %v", err)
		}
		return db, cleanup
	}

	parsed := testPostgresURL(tb)
	key = fmt.Sprintf("%s|%#v", key, namingStrategy(gormConfig))
	schema := takePooledSchema(key)
	if schema == nil {
		schema = preparePooledSchema(tb, parsed, gormConfig, prepare)
	}
	if gormConfig == nil {
		gormConfig = &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	}
	db := openPostgres(tb, schemaDSN(parsed, schema.name), gormConfig)
	var once sync.Once
	return db, func() {
		once.Do(func() {
			closePostgres(tb, db)
			if err := schema.reset(); err != nil {
				schema.drop()
				return
			}
			schemaPool.mu.Lock()
			schemaPool.free[key] = append(schemaPool.free[key], schema)
			schemaPool.mu.Unlock()
		})
	}
}

func takePooledSchema(key string) *pooledSchema {
	schemaPool.mu.Lock()
	defer schemaPool.mu.Unlock()
	free := schemaPool.free[key]
	if len(free) == 0 {
		return nil
	}
	schema := free[len(free)-1]
	schemaPool.free[key] = free[:len(free)-1]
	return schema
}

func preparePooledSchema(tb testing.TB, parsed *url.URL, gormConfig *gorm.Config, prepare func(*gorm.DB) error) *pooledSchema {
	tb.Helper()
	name := postgresResourceName("schema")
	maintenance := openPostgres(tb, schemaDSN(parsed, name), &gorm.Config{
		Logger:               logger.Default.LogMode(logger.Silent),
		DisableAutomaticPing: true,
	})
	schema := &pooledSchema{name: name, maintenance: maintenance}
	schemaPool.mu.Lock()
	schemaPool.all = append(schemaPool.all, schema)
	schemaPool.mu.Unlock()

	if err := maintenance.Exec(`CREATE SCHEMA ` + quote(name)).Error; err != nil {
		tb.Fatalf("create pooled PostgreSQL schema: %v", err)
	}
	db := openPostgres(tb, schemaDSN(parsed, name), preparationConfig(gormConfig))
	err := prepare(db)
	closePostgres(tb, db)
	if err != nil {
		tb.Fatalf("prepare pooled PostgreSQL schema: %v", err)
	}
	if err := schema.snapshot(); err != nil {
		tb.Fatalf("snapshot pooled PostgreSQL schema: %v", err)
	}
	return schema
}

// snapshot records the prepared structure, copies the prepared rows aside, and notes the
// sequence values, so reset can return the schema to this state.
func (s *pooledSchema) snapshot() error {
	fingerprint, err := s.structure()
	if err != nil {
		return err
	}
	s.fingerprint = fingerprint
	tables, err := s.tables()
	if err != nil {
		return err
	}
	snapshot := quote(s.name + "_seed")
	for _, table := range tables {
		var rows int64
		if err = s.maintenance.Raw(`SELECT count(*) FROM ` + quote(table)).Scan(&rows).Error; err != nil {
			return err
		}
		if rows == 0 {
			continue
		}
		if len(s.seeded) == 0 {
			if err = s.maintenance.Exec(`CREATE SCHEMA ` + snapshot).Error; err != nil {
				return err
			}
		}
		if err = s.maintenance.Exec(`CREATE TABLE ` + snapshot + `.` + quote(table) + ` AS TABLE ` + quote(table)).Error; err != nil {
			return err
		}
		s.seeded = append(s.seeded, table)
	}
	if err = s.maintenance.Raw(`SELECT sequencename AS name, coalesce(last_value, start_value) AS value,
		last_value IS NOT NULL AS called
		FROM pg_sequences WHERE schemaname = current_schema()`).Scan(&s.sequences).Error; err != nil {
		return err
	}
	order, err := s.referencesFirst(tables)
	if err != nil {
		return err
	}
	s.deleteOrder = order
	return nil
}

// referencesFirst orders tables so each one comes before every table it references. DELETE then
// never trips a restricting foreign key; a cycle leaves its tables in name order and reset falls
// back to dropping the schema if that fails.
func (s *pooledSchema) referencesFirst(tables []string) ([]string, error) {
	var edges []struct {
		Child  string
		Parent string
	}
	if err := s.maintenance.Raw(`SELECT DISTINCT child.relname AS child, parent.relname AS parent
		FROM pg_constraint c
		JOIN pg_class child ON child.oid = c.conrelid
		JOIN pg_class parent ON parent.oid = c.confrelid
		WHERE c.contype = 'f' AND c.connamespace = current_schema()::regnamespace
		AND child.oid <> parent.oid`).Scan(&edges).Error; err != nil {
		return nil, err
	}
	referencedBy := map[string][]string{}
	for _, edge := range edges {
		referencedBy[edge.Parent] = append(referencedBy[edge.Parent], edge.Child)
	}
	order := make([]string, 0, len(tables))
	state := map[string]int{}
	var visit func(string)
	visit = func(table string) {
		if state[table] != 0 {
			return
		}
		state[table] = 1
		for _, child := range referencedBy[table] {
			visit(child)
		}
		state[table] = 2
		order = append(order, table)
	}
	for _, table := range tables {
		visit(table)
	}
	// visit appends every table referencing a table before the table itself.
	return order, nil
}

// reset returns the schema to its prepared state, or fails when the test changed its structure.
func (s *pooledSchema) reset() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := s.maintenance.WithContext(ctx)
	fingerprint, err := s.structureOn(db)
	if err != nil {
		return err
	}
	if fingerprint != s.fingerprint {
		return fmt.Errorf("schema %s changed structure", s.name)
	}
	// DELETE, unlike TRUNCATE, costs next to nothing on the many tables a test left empty.
	var statements strings.Builder
	statements.WriteString(`SET LOCAL lock_timeout = '5s';`)
	for _, table := range s.deleteOrder {
		statements.WriteString(`DELETE FROM ` + quote(table) + `;`)
	}
	for _, table := range s.seeded {
		statements.WriteString(`INSERT INTO ` + quote(table) + ` SELECT * FROM ` + quote(s.name+"_seed") + `.` + quote(table) + `;`)
	}
	for _, sequence := range s.sequences {
		fmt.Fprintf(&statements, `SELECT setval('%s.%s', %d, %t);`,
			strings.ReplaceAll(quote(s.name), `'`, `''`), strings.ReplaceAll(quote(sequence.Name), `'`, `''`),
			sequence.Value, sequence.Called)
	}
	// A test that leaked a connection must not hang the suite: the lock timeout fails the reset
	// and the schema is dropped instead.
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.Exec(statements.String()).Error
	})
}

func (s *pooledSchema) drop() {
	schemaPool.mu.Lock()
	for index, pooled := range schemaPool.all {
		if pooled == s {
			schemaPool.all = append(schemaPool.all[:index], schemaPool.all[index+1:]...)
			break
		}
	}
	schemaPool.mu.Unlock()
	s.dropSchemas()
}

func (s *pooledSchema) dropSchemas() {
	_ = s.maintenance.Exec(`DROP SCHEMA IF EXISTS ` + quote(s.name+"_seed") + ` CASCADE`).Error
	_ = s.maintenance.Exec(`DROP SCHEMA IF EXISTS ` + quote(s.name) + ` CASCADE`).Error
	if pool, err := s.maintenance.DB(); err == nil {
		_ = pool.Close()
	}
}

func (s *pooledSchema) tables() ([]string, error) { return s.tablesOn(s.maintenance) }

func (s *pooledSchema) tablesOn(db *gorm.DB) ([]string, error) {
	var tables []string
	err := db.Raw(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename`).
		Scan(&tables).Error
	return tables, err
}

func (s *pooledSchema) structure() (string, error) { return s.structureOn(s.maintenance) }

// structureOn digests the schema's tables, columns, indexes, constraints, and sequences.
func (s *pooledSchema) structureOn(db *gorm.DB) (string, error) {
	var parts []string
	err := db.Raw(`
		SELECT 'c ' || table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' ||
			coalesce(column_default, '')
		FROM information_schema.columns WHERE table_schema = current_schema()
		UNION ALL
		SELECT 'i ' || indexdef FROM pg_indexes WHERE schemaname = current_schema()
		UNION ALL
		SELECT 'k ' || conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
		FROM pg_constraint WHERE connamespace = current_schema()::regnamespace
		UNION ALL
		SELECT 's ' || sequencename FROM pg_sequences WHERE schemaname = current_schema()
		ORDER BY 1`).Scan(&parts).Error
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(digest[:]), nil
}

func dropSchemaPool() {
	schemaPool.mu.Lock()
	all := schemaPool.all
	schemaPool.all = nil
	schemaPool.free = map[string][]*pooledSchema{}
	schemaPool.enabled = false
	schemaPool.mu.Unlock()
	for _, schema := range all {
		schema.dropSchemas()
	}
}

// openAutoMigrated is OpenPostgresWithCleanup's pooled path: the models name the schema.
func openAutoMigrated(tb testing.TB, gormConfig *gorm.Config, models []any) (*gorm.DB, func()) {
	tb.Helper()
	names := make([]string, len(models))
	for index, model := range models {
		names[index] = reflect.TypeOf(model).String()
	}
	return openPooled(tb, gormConfig, "automigrate:"+strings.Join(names, ","), func(db *gorm.DB) error {
		return db.AutoMigrate(models...)
	})
}

func namingStrategy(gormConfig *gorm.Config) any {
	if gormConfig == nil {
		return nil
	}
	return gormConfig.NamingStrategy
}

// preparationConfig copies the caller's config for the preparing connection. gorm.Open keeps and
// fills in the config it is given, so the copy is taken before the caller's own connection opens.
func preparationConfig(gormConfig *gorm.Config) *gorm.Config {
	if gormConfig == nil {
		return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	}
	copied := *gormConfig
	return &copied
}

func schemaDSN(parsed *url.URL, schema string) string {
	scoped := *parsed
	parameters := scoped.Query()
	parameters.Set("search_path", schema)
	parameters.Set("timezone", "UTC")
	scoped.RawQuery = parameters.Encode()
	return scoped.String()
}

func quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func poolEnabled() bool {
	schemaPool.mu.Lock()
	defer schemaPool.mu.Unlock()
	return schemaPool.enabled
}
