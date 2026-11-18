package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const (
	databaseFileName = "overtok.db"
	busyTimeoutMS    = 5000
)

// DB is Overtok's local SQLite database.
type DB struct {
	sql       *sql.DB
	closeOnce sync.Once
	closeErr  error
	closed    chan struct{}
}

// DefaultPath returns the platform-appropriate application database path.
func DefaultPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(configDir, "Overtok", databaseFileName), nil
}

// Open prepares a private database file, configures SQLite, and applies all
// supported migrations before returning.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open local database: %w", err)
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("open local database: path must be absolute")
	}
	if err := prepareDatabaseFile(path); err != nil {
		return nil, err
	}

	sqlDB, err := driver.Open(dataSourceName(path))
	if err != nil {
		return nil, fmt.Errorf("configure local database: %w", err)
	}
	db := &DB{sql: sqlDB, closed: make(chan struct{})}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open local database: %w", err)
	}
	if err := migrate(ctx, sqlDB, applicationMigrations); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Close stops accepting work and waits for active database operations. If the
// context expires, closure continues in the background so handles are not leaked.
func (db *DB) Close(ctx context.Context) error {
	db.closeOnce.Do(func() {
		go func() {
			db.closeErr = db.sql.Close()
			close(db.closed)
		}()
	})
	select {
	case <-db.closed:
		if db.closeErr != nil {
			return fmt.Errorf("close local database: %w", db.closeErr)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close local database: %w", ctx.Err())
	}
}

// SchemaVersion returns the latest applied local schema version.
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	version, err := currentVersion(ctx, db.sql)
	if err != nil {
		return 0, fmt.Errorf("read local schema version: %w", err)
	}
	return version, nil
}

func prepareDatabaseFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create local database directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure local database directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepare local database: symbolic links are not allowed")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local database: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create local database: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure local database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close prepared local database: %w", err)
	}
	return nil
}

func dataSourceName(path string) string {
	query := url.Values{}
	// These connection-local PRAGMAs apply to every pooled connection. WAL is
	// persistent, while foreign keys and busy handling are not.
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}
