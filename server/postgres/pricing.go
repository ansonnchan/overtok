package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsurePricingRevision registers a trusted bundled pricing snapshot and
// rejects a changed catalog reusing an existing revision name.
func EnsurePricingRevision(ctx context.Context, pool *pgxpool.Pool, registry *modelcatalog.PricingRegistry) error {
	if pool == nil || registry == nil || registry.Revision == "" || registry.EffectiveAt().IsZero() {
		return errors.New("register PostgreSQL pricing revision: complete registry and pool are required")
	}
	checksum, err := registry.Checksum()
	if err != nil {
		return err
	}
	var stored []byte
	err = pool.QueryRow(ctx, `SELECT catalog_checksum FROM pricing_revisions WHERE revision = $1`, registry.Revision).Scan(&stored)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := pool.Exec(ctx, `
			INSERT INTO pricing_revisions(revision, catalog_checksum, effective_at)
			VALUES ($1, $2, $3)
		`, registry.Revision, checksum[:], registry.EffectiveAt()); err != nil {
			return fmt.Errorf("insert PostgreSQL pricing revision: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("read PostgreSQL pricing revision: %w", err)
	case !bytes.Equal(stored, checksum[:]):
		return errors.New("register PostgreSQL pricing revision: checksum differs for existing revision")
	default:
		return nil
	}
}
