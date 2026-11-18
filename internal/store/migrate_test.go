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
