package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/tokenholic/internal/normalize"
	"github.com/ansonnchan/tokenholic/internal/store"
	"github.com/ncruces/go-sqlite3"
)

func TestLocalDatabaseProbeChecksTemporaryDatabaseWithoutRenderingPath(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private", "state.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := (LocalDatabaseProbe{Path: path, Default: true, Now: func() time.Time {
		return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	}}).Check(t.Context())
	if status.State != DatabaseReady || !status.Open || !status.IntegrityOK || !status.Stale {
		t.Fatalf("Check() = %#v", status)
	}
	report := Report{GOOS: "darwin", GOARCH: "arm64", Database: status}
	output := report.Render()
	if strings.Contains(output, path) || !strings.Contains(output, "path redacted") || !strings.Contains(output, "Last collection: never") {
		t.Fatalf("database output = %q", output)
	}
	if report.ExitCode() != 1 {
		t.Fatalf("stale database exit = %d, want 1", report.ExitCode())
	}
}

func TestLocalDatabaseProbeClassifiesTemporaryCorruptDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	if err := os.WriteFile(path, []byte("synthetic non-database"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := (LocalDatabaseProbe{Path: path}).Check(t.Context())
	if status.State != DatabaseCorrupt {
		t.Fatalf("corrupt Check() = %#v", status)
	}
	report := Report{GOOS: "darwin", GOARCH: "arm64", Database: status}
	if report.ExitCode() != 2 || !strings.Contains(report.Render(), "restore or replace") {
		t.Fatalf("corrupt report = %q code=%d", report.Render(), report.ExitCode())
	}
}

func TestDatabaseFailureStatesAreActionable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	last := now.Add(-48 * time.Hour)
	tests := []struct {
		name   string
		status DatabaseStatus
		want   string
		code   int
	}{
		{name: "locked", status: DatabaseStatus{Location: "custom application data", State: DatabaseLocked}, want: "close other tokenholic processes", code: 2},
		{name: "incompatible", status: DatabaseStatus{Location: "default application data", State: DatabaseIncompatible, SupportedVersion: 3}, want: "update tokenholic", code: 2},
		{name: "not initialized", status: DatabaseStatus{Location: "default application data", State: DatabaseNotInitialized}, want: "run `tokenholic`", code: 1},
		{name: "stale", status: DatabaseStatus{Location: "default application data", State: DatabaseReady, IntegrityOK: true, LastSuccessfulCollection: &last, Stale: true}, want: "stale; run a local refresh", code: 1},
		{name: "failed adapter", status: DatabaseStatus{Location: "default application data", State: DatabaseReady, IntegrityOK: true, LastSuccessfulCollection: &now, AdapterHealth: []store.StoredAdapterHealth{{Health: normalize.SourceHealth{Source: normalize.SourceCodex, Surface: normalize.SurfaceCLI, Status: normalize.HealthError}}}}, want: "Adapter codex/cli: error", code: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := Report{GOOS: "darwin", GOARCH: "arm64", Database: test.status}
			if output := report.Render(); !strings.Contains(output, test.want) || report.ExitCode() != test.code {
				t.Fatalf("Render() = %q, ExitCode() = %d", output, report.ExitCode())
			}
		})
	}
}

func TestClassifyDatabaseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want DatabaseState
	}{
		{err: store.ErrIncompatibleSchema, want: DatabaseIncompatible},
		{err: sqlite3.BUSY, want: DatabaseLocked},
		{err: sqlite3.LOCKED, want: DatabaseLocked},
		{err: sqlite3.CORRUPT, want: DatabaseCorrupt},
		{err: sqlite3.NOTADB, want: DatabaseCorrupt},
		{err: errors.New("synthetic"), want: DatabaseError},
	}
	for _, test := range tests {
		if got := classifyDatabaseError(test.err); got != test.want {
			t.Errorf("classifyDatabaseError(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestMissingTemporaryDatabaseIsNotInitialized(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing.db")
	status := (LocalDatabaseProbe{Path: path}).Check(context.Background())
	if status.State != DatabaseNotInitialized {
		t.Fatalf("Check() = %#v", status)
	}
}
