package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ansonnchan/tokenholic/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 0x4f564552544f4b

// ErrIncompatibleSchema marks a database whose applied migration history does
// not match this build.
var ErrIncompatibleSchema = errors.New("incompatible PostgreSQL schema")

type appliedMigration struct {
	version  int
	name     string
	checksum string
}

// Migrate applies every pending server migration in one locked transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) (err error) {
	if pool == nil {
		return errors.New("migrate PostgreSQL: pool is required")
	}
	available, err := migrations.All()
	if err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin PostgreSQL migration: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock PostgreSQL migrations: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("initialize PostgreSQL migration history: %w", err)
	}
	applied, err := readApplied(ctx, tx)
	if err != nil {
		return err
	}
	if err = validateApplied(applied, available); err != nil {
		return err
	}
	for _, migration := range available[len(applied):] {
		// Migration SQL is embedded, reviewed input. Simple protocol permits a
		// migration to contain multiple DDL statements in one transaction.
		if _, err = tx.Exec(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("apply PostgreSQL migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if _, err = tx.Exec(ctx,
			`INSERT INTO schema_migrations(version, name, checksum) VALUES ($1, $2, $3)`,
			migration.Version, migration.Name, migration.Checksum); err != nil {
			return fmt.Errorf("record PostgreSQL migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostgreSQL migration: %w", err)
	}
	return nil
}

func readApplied(ctx context.Context, tx pgx.Tx) ([]appliedMigration, error) {
	rows, err := tx.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL migration history: %w", err)
	}
	defer rows.Close()
	var applied []appliedMigration
	for rows.Next() {
		var migration appliedMigration
		if err := rows.Scan(&migration.version, &migration.name, &migration.checksum); err != nil {
			return nil, fmt.Errorf("decode PostgreSQL migration history: %w", err)
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL migration history: %w", err)
	}
	return applied, nil
}

func validateApplied(applied []appliedMigration, available []migrations.Migration) error {
	if len(applied) > len(available) {
		return fmt.Errorf("%w: database version %d is newer than supported version %d", ErrIncompatibleSchema, applied[len(applied)-1].version, len(available))
	}
	for index, actual := range applied {
		expected := available[index]
		if actual.version != expected.Version || actual.name != expected.Name || actual.checksum != expected.Checksum {
			return fmt.Errorf("%w: migration history differs at version %d", ErrIncompatibleSchema, expected.Version)
		}
	}
	return nil
}
