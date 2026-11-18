package store

import (
	"context"
	"fmt"
	"time"
)

// DiagnosticSnapshot contains only path-redacted local storage health.
type DiagnosticSnapshot struct {
	IntegrityOK              bool
	SchemaVersion            int
	SupportedSchemaVersion   int
	CheckpointCount          int
	LastSuccessfulCollection *time.Time
	AdapterHealth            []StoredAdapterHealth
	PendingOutbox            int
}

// SupportedSchemaVersion returns the newest migration understood by this build.
func SupportedSchemaVersion() int {
	return len(applicationMigrations)
}

// Diagnostics checks database integrity and summarizes durable local state.
func (db *DB) Diagnostics(ctx context.Context) (DiagnosticSnapshot, error) {
	snapshot := DiagnosticSnapshot{SupportedSchemaVersion: SupportedSchemaVersion()}
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	snapshot.SchemaVersion = version

	rows, err := db.sql.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return DiagnosticSnapshot{}, fmt.Errorf("check local database integrity: %w", err)
	}
	integrityOK := true
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			_ = rows.Close()
			return DiagnosticSnapshot{}, fmt.Errorf("decode integrity result: %w", err)
		}
		if result != "ok" {
			integrityOK = false
		}
	}
	if err := rows.Close(); err != nil {
		return DiagnosticSnapshot{}, fmt.Errorf("close integrity results: %w", err)
	}
	snapshot.IntegrityOK = integrityOK

	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM source_checkpoints`).Scan(&snapshot.CheckpointCount); err != nil {
		return DiagnosticSnapshot{}, fmt.Errorf("count source checkpoints: %w", err)
	}
	snapshot.AdapterHealth, err = db.AdapterHealthRecords(ctx)
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	for _, health := range snapshot.AdapterHealth {
		if health.LastSuccessAt != nil && (snapshot.LastSuccessfulCollection == nil || health.LastSuccessAt.After(*snapshot.LastSuccessfulCollection)) {
			value := *health.LastSuccessAt
			snapshot.LastSuccessfulCollection = &value
		}
	}
	snapshot.PendingOutbox, err = db.PendingOutboxCount(ctx)
	if err != nil {
		return DiagnosticSnapshot{}, err
	}
	return snapshot, nil
}
