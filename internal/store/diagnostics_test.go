package store

import (
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/normalize"
)

func TestDiagnosticsSummarizesRedactedLocalState(t *testing.T) {
	t.Parallel()
	db := openStoreForTest(t)
	batch := sampleBatch()
	if _, err := db.CommitObservations(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	bucket := completeBucket(time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC), normalize.SourceClaude, normalize.SurfaceCLI, "synthetic-model", 1, 1)
	if _, err := db.ApplyBucketRevisions(t.Context(), []UsageBucket{bucket}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Diagnostics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.IntegrityOK || snapshot.SchemaVersion != SupportedSchemaVersion() || snapshot.CheckpointCount != 1 || snapshot.PendingOutbox != 1 {
		t.Fatalf("Diagnostics() = %#v", snapshot)
	}
	if snapshot.LastSuccessfulCollection == nil || len(snapshot.AdapterHealth) != 1 {
		t.Fatalf("diagnostic collection state = %#v", snapshot)
	}
}
