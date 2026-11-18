package doctor

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/ansonnchan/overtok/internal/store"
	"github.com/ncruces/go-sqlite3"
)

// DatabaseState is a stable, path-redacted storage diagnostic state.
type DatabaseState string

const (
	// DatabaseReady means the database opened and passed integrity checks.
	DatabaseReady DatabaseState = "ready"
	// DatabaseNotInitialized means no local database file exists yet.
	DatabaseNotInitialized DatabaseState = "not_initialized"
	// DatabaseLocked means another writer prevented a bounded open.
	DatabaseLocked DatabaseState = "locked"
	// DatabaseCorrupt means SQLite rejected or failed the database image.
	DatabaseCorrupt DatabaseState = "corrupt"
	// DatabaseIncompatible means the schema is newer or has changed history.
	DatabaseIncompatible DatabaseState = "incompatible"
	// DatabaseError means another open or diagnostic operation failed.
	DatabaseError DatabaseState = "error"
)

// DatabaseStatus summarizes local storage without retaining its path.
type DatabaseStatus struct {
	Location                 string
	State                    DatabaseState
	Open                     bool
	IntegrityOK              bool
	SchemaVersion            int
	SupportedVersion         int
	CheckpointCount          int
	LastSuccessfulCollection *time.Time
	Stale                    bool
	AdapterHealth            []store.StoredAdapterHealth
	PendingOutbox            int
}

// DatabaseProbe supplies a redacted local storage status.
type DatabaseProbe interface {
	Check(context.Context) DatabaseStatus
}

// LocalDatabaseProbe checks one configured local SQLite database.
type LocalDatabaseProbe struct {
	Path       string
	Default    bool
	Now        func() time.Time
	StaleAfter time.Duration
}

// Check opens and inspects the database without exposing its configured path.
func (p LocalDatabaseProbe) Check(ctx context.Context) DatabaseStatus {
	status := DatabaseStatus{Location: "custom application data", SupportedVersion: store.SupportedSchemaVersion()}
	if p.Default {
		status.Location = "default application data"
	}
	if _, err := os.Stat(p.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			status.State = DatabaseNotInitialized
		} else {
			status.State = DatabaseError
		}
		return status
	}
	db, err := store.Open(ctx, p.Path)
	if err != nil {
		status.State = classifyDatabaseError(err)
		return status
	}
	status.Open = true
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = db.Close(closeCtx)
	}()
	snapshot, err := db.Diagnostics(ctx)
	if err != nil {
		status.State = classifyDatabaseError(err)
		return status
	}
	status.IntegrityOK = snapshot.IntegrityOK
	status.SchemaVersion = snapshot.SchemaVersion
	status.SupportedVersion = snapshot.SupportedSchemaVersion
	status.CheckpointCount = snapshot.CheckpointCount
	status.LastSuccessfulCollection = snapshot.LastSuccessfulCollection
	status.AdapterHealth = snapshot.AdapterHealth
	status.PendingOutbox = snapshot.PendingOutbox
	if !snapshot.IntegrityOK {
		status.State = DatabaseCorrupt
		return status
	}
	status.State = DatabaseReady
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	staleAfter := p.StaleAfter
	if staleAfter <= 0 {
		staleAfter = 24 * time.Hour
	}
	status.Stale = status.LastSuccessfulCollection == nil || now.Sub(*status.LastSuccessfulCollection) > staleAfter
	return status
}

func classifyDatabaseError(err error) DatabaseState {
	switch {
	case errors.Is(err, store.ErrIncompatibleSchema):
		return DatabaseIncompatible
	case errors.Is(err, sqlite3.BUSY), errors.Is(err, sqlite3.LOCKED):
		return DatabaseLocked
	case errors.Is(err, sqlite3.CORRUPT), errors.Is(err, sqlite3.NOTADB):
		return DatabaseCorrupt
	default:
		return DatabaseError
	}
}
