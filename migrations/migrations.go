package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Migration is one immutable PostgreSQL schema change.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

//go:embed postgres/*.sql
var migrationFiles embed.FS

// All returns every embedded migration in version order.
func All() ([]Migration, error) {
	paths, err := fs.Glob(migrationFiles, "postgres/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list PostgreSQL migrations: %w", err)
	}
	sort.Strings(paths)
	migrations := make([]Migration, 0, len(paths))
	for _, path := range paths {
		base := strings.TrimSuffix(strings.TrimPrefix(path, "postgres/"), ".sql")
		versionText, name, found := strings.Cut(base, "_")
		if !found || len(versionText) != 4 || name == "" {
			return nil, fmt.Errorf("invalid PostgreSQL migration name %q", path)
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return nil, fmt.Errorf("parse PostgreSQL migration version %q: %w", path, err)
		}
		contents, err := migrationFiles.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read PostgreSQL migration %q: %w", path, err)
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, Migration{
			Version: version, Name: name, SQL: string(contents), Checksum: hex.EncodeToString(digest[:]),
		})
	}
	for index, migration := range migrations {
		if migration.Version != index+1 {
			return nil, fmt.Errorf("PostgreSQL migrations are not contiguous at version %d", index+1)
		}
	}
	return migrations, nil
}
