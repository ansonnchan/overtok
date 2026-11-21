package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

func TestMigrateCreatesUpgradesAndRepeats(t *testing.T) {
	t.Parallel()
	db := openTestSQLDB(t)
	migrations := []migration{
		{version: 1, name: "one", sql: `CREATE TABLE one (id INTEGER PRIMARY KEY) STRICT;`},
		{version: 2, name: "two", sql: `CREATE TABLE two (id INTEGER PRIMARY KEY) STRICT;`},
	}
	if err := migrate(t.Context(), db, migrations[:1]); err != nil {
		t.Fatal(err)
	}
	if err := migrate(t.Context(), db, migrations); err != nil {
		t.Fatal(err)
	}
	if err := migrate(t.Context(), db, migrations); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration count = %d, want 2", count)
	}
}

func TestMigrateRejectsIncompatibleVersionsAndHistory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		applied appliedMigration
	}{
		{name: "newer version", applied: appliedMigration{version: 2, name: "future", checksum: "future"}},
		{name: "changed migration", applied: appliedMigration{version: 1, name: "changed", checksum: "changed"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openTestSQLDB(t)
			createMigrationTable(t, db)
			if _, err := db.ExecContext(t.Context(),
				`INSERT INTO schema_migrations(version, name, checksum) VALUES (?, ?, ?)`,
				test.applied.version, test.applied.name, test.applied.checksum); err != nil {
				t.Fatal(err)
			}
			err := migrate(t.Context(), db, []migration{{version: 1, name: "one", sql: `SELECT 1;`}})
			if !errors.Is(err, ErrIncompatibleSchema) {
				t.Fatalf("migrate() error = %v, want ErrIncompatibleSchema", err)
			}
		})
	}
}

func TestMigrateRollsBackPendingBatch(t *testing.T) {
	t.Parallel()
	db := openTestSQLDB(t)
	err := migrate(t.Context(), db, []migration{
		{version: 1, name: "one", sql: `CREATE TABLE should_rollback (id INTEGER PRIMARY KEY) STRICT;`},
		{version: 2, name: "broken", sql: `CREATE TABLE`},
	})
	if err == nil {
		t.Fatal("migrate() succeeded")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('schema_migrations', 'should_rollback')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("tables after rollback = %d, want 0", count)
	}
}

func TestMigrateHonorsCancellation(t *testing.T) {
	t.Parallel()
	db := openTestSQLDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := migrate(ctx, db, []migration{{version: 1, name: "one", sql: `SELECT 1;`}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("migrate(canceled) error = %v, want context cancellation", err)
	}
}

func TestCacheWriteLifetimeMigrationRemovesLocalNoticeUsage(t *testing.T) {
	t.Parallel()
	db := openTestSQLDB(t)
	if err := migrate(t.Context(), db, applicationMigrations[:3]); err != nil {
		t.Fatal(err)
	}
	const insertObservation = `
		INSERT INTO observations(
			identity, source, surface, source_version, occurred_at, observed_model_id,
			model_resolution, input_cache_write_tokens, count_semantics, measurement,
			adapter_version, normalization_version, warnings_json
		) VALUES (?, 'claude', 'cli', 'synthetic-v1', '2026-01-06T03:00:00Z', ?, 'unknown', '40',
			'incremental', 'reported', '1', '1', '[]')`
	const insertBucket = `
		INSERT INTO usage_buckets(
			bucket_key, bucket_start, interval_seconds, source, surface, observed_model_id,
			model_resolution, measurement, normalization_version, input_uncached_state,
			input_cache_read_state, input_cache_write_state, output_state,
			reasoning_output_state, api_estimate_state, revision, payload_json
		) VALUES (?, '2026-01-06T03:00:00Z', 60, 'claude', 'cli', ?, 'unknown', 'reported', '1',
			'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable', '1', '{}')`
	for key, model := range map[string]string{"kept": "claude-sonnet-synthetic", "removed": "<synthetic>"} {
		if _, err := db.ExecContext(t.Context(), insertObservation, key, model); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), insertBucket, key, model); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(),
			`INSERT INTO outbox_records(bucket_key, revision, payload_json) VALUES (?, '1', '{}')`, key); err != nil {
			t.Fatal(err)
		}
	}

	// Stop at the lifetime migration: a later one rebuilds the bucket tables.
	if err := migrate(t.Context(), db, applicationMigrations[:4]); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"observations", "usage_buckets", "outbox_records"} {
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s rows after migration = %d, want only the real model", table, count)
		}
	}
	// Existing rows keep their combined total with an unknown time-to-live.
	var total string
	var fiveMinute, oneHour sql.NullString
	if err := db.QueryRowContext(t.Context(), `
		SELECT input_cache_write_tokens, input_cache_write_5m_tokens, input_cache_write_1h_tokens
		FROM observations`).Scan(&total, &fiveMinute, &oneHour); err != nil {
		t.Fatal(err)
	}
	if total != "40" || fiveMinute.Valid || oneHour.Valid {
		t.Fatalf("migrated cache write = %q/%v/%v, want 40 with unknown lifetimes", total, fiveMinute, oneHour)
	}
}

func TestSyncV2MigrationRebuildsBucketsAndKeepsSourceEvidence(t *testing.T) {
	t.Parallel()
	db := openTestSQLDB(t)
	if err := migrate(t.Context(), db, applicationMigrations[:4]); err != nil {
		t.Fatal(err)
	}
	const insertObservation = `
		INSERT INTO observations(
			identity, source, surface, source_version, occurred_at, observed_model_id,
			canonical_model_id, model_provider, pricing_key, model_resolution, output_tokens,
			count_semantics, measurement, adapter_version, normalization_version, warnings_json
		) VALUES (?, ?, 'cli', 'synthetic-v1', '2026-01-06T03:00:00Z', ?, ?, ?, ?, ?, '7',
			'incremental', 'reported', '1', '1', ?)`
	rows := []struct {
		identity, source, model string
		canonical, pricing      any
		resolution, warnings    string
		wantEvidence            string
	}{
		{"resolved", "claude", "model-latest", "model-a", "synthetic/model-a", "resolved", `[]`, "reported"},
		{"uncatalogued", "claude", "model-new", nil, nil, "unknown", `[{"code":"model_unresolved"}]`, "reported"},
		{"inferred-known", "codex", "model-b", "model-b", nil, "inferred", `[{"code":"model_inferred"}]`, "inferred"},
		{"inferred-uncatalogued", "codex", "model-c", nil, nil, "unknown", `[{"code":"model_inferred"},{"code":"model_unresolved"}]`, "inferred"},
		{"no-model", "codex", "", nil, nil, "unknown", `[{"code":"model_unavailable"}]`, "unknown"},
	}
	for _, row := range rows {
		var provider any
		if row.canonical != nil {
			provider = "synthetic"
		}
		if _, err := db.ExecContext(t.Context(), insertObservation, row.identity, row.source, row.model,
			row.canonical, provider, row.pricing, row.resolution, row.warnings); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO usage_buckets(
			bucket_key, bucket_start, interval_seconds, source, surface, observed_model_id,
			model_resolution, measurement, normalization_version, input_uncached_state,
			input_cache_read_state, input_cache_write_state, output_state,
			reasoning_output_state, api_estimate_state, revision, payload_json
		) VALUES ('old', '2026-01-06T03:00:00Z', 60, 'claude', 'cli', 'model-new', 'unknown', 'reported', '1',
			'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable', 'unavailable', '9', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO outbox_records(bucket_key, revision, payload_json) VALUES ('old', '9', '{}')`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(t.Context(), db, applicationMigrations); err != nil {
		t.Fatal(err)
	}
	// Buckets are derived state and are rebuilt by the next refresh.
	for _, table := range []string{"usage_buckets", "outbox_records"} {
		var count int
		if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after migration = %d, want 0", table, count)
		}
	}
	for _, row := range rows {
		var evidence string
		var canonical, provider, pricing sql.NullString
		if err := db.QueryRowContext(t.Context(), `
			SELECT model_resolution, canonical_model_id, model_provider, pricing_key
			FROM observations WHERE identity = ?`, row.identity).Scan(&evidence, &canonical, &provider, &pricing); err != nil {
			t.Fatal(err)
		}
		if evidence != row.wantEvidence || canonical.Valid || provider.Valid || pricing.Valid {
			t.Errorf("%s: evidence = %q, catalog columns = %v/%v/%v; want %q and none",
				row.identity, evidence, canonical, provider, pricing, row.wantEvidence)
		}
	}
}

func openTestSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), databaseFileName)
	db, err := driver.Open(dataSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createMigrationTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		) STRICT
	`); err != nil {
		t.Fatal(err)
	}
}
