package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/modelcatalog"
	"github.com/ansonnchan/overtok/internal/normalize"
	"github.com/ansonnchan/overtok/internal/store"
)

func TestOrchestratorRepeatedAndReversedScansAreIdentical(t *testing.T) {
	t.Parallel()
	findings := syntheticFindings(t)
	fixedNow := time.Date(2026, time.November, 17, 20, 0, 0, 0, time.UTC)

	firstDB := openCollectionStore(t)
	first := Orchestrator{
		Discovery: staticDiscovery(findings), Collector: Collector{SourceVersion: "synthetic-v1"},
		Store: firstDB, AdapterVersion: "native-test-v1", Now: func() time.Time { return fixedNow },
	}
	firstResult, err := first.Scan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.InsertedObservations != 4 {
		t.Fatalf("first inserted observations = %d, want 4", firstResult.InsertedObservations)
	}
	before, err := firstDB.Observations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := first.Scan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if repeated.InsertedObservations != 0 {
		t.Fatalf("repeated inserted observations = %d, want 0", repeated.InsertedObservations)
	}
	after, err := firstDB.Observations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("repeated scan changed observations:\nbefore=%#v\nafter=%#v", before, after)
	}

	reversedFindings := reverseFindings(findings)
	reversedDB := openCollectionStore(t)
	reversed := Orchestrator{
		Discovery: staticDiscovery(reversedFindings), Collector: Collector{SourceVersion: "synthetic-v1"},
		Store: reversedDB, AdapterVersion: "native-test-v1", Now: func() time.Time { return fixedNow },
	}
	if _, err := reversed.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	reversedObservations, err := reversedDB.Observations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, reversedObservations) {
		t.Fatalf("candidate order changed stored observations:\nfirst=%#v\nreversed=%#v", before, reversedObservations)
	}
}

func TestOrchestratorPersistsValidSourcesWhenAnotherIsPartial(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	codexRoot := filepath.Join(root, "codex")
	copyFixture(t, filepath.Join("claude-code", "malformed-middle.jsonl"), filepath.Join(claudeRoot, "malformed.jsonl"))
	copyFixture(t, filepath.Join("codex-desktop", "basic.jsonl"), filepath.Join(codexRoot, "basic.jsonl"))
	discovery := adapters.Discoverer{Roots: []adapters.RootSpec{
		testRoot(adapters.RootClaudeCLI, normalize.SourceClaude, normalize.SurfaceCLI, claudeRoot),
		testRoot(adapters.RootCodexShared, normalize.SourceCodex, normalize.SurfaceSharedLocal, codexRoot),
	}}
	db := openCollectionStore(t)
	resolver, err := modelcatalog.NewResolver(modelcatalog.Catalog{
		Revision: "synthetic-r1",
		Models: []modelcatalog.Model{
			{CanonicalID: "claude-sonnet-synthetic", Provider: "synthetic-provider"},
			{CanonicalID: "gpt-6-synthetic", Provider: "synthetic-provider"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Orchestrator{
		Discovery: discovery, Collector: Collector{SourceVersion: "synthetic-v1"}, Models: resolver, Store: db,
		Now: func() time.Time { return time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC) },
	}).Scan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(result.Warnings, "malformed_record") {
		t.Fatalf("warnings = %#v, want malformed_record", result.Warnings)
	}
	observations, err := db.Observations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sources := map[normalize.Source]int{}
	for _, observation := range observations {
		sources[observation.Source]++
		if observation.CanonicalModelID == nil || observation.ModelProvider == nil {
			t.Fatalf("observation was not model-resolved: %#v", observation)
		}
	}
	if sources[normalize.SourceClaude] != 2 || sources[normalize.SourceCodex] != 1 {
		t.Fatalf("stored source counts = %#v", sources)
	}
	health, err := db.AdapterHealthRecords(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range health {
		if record.Health.Source == normalize.SourceClaude && record.Health.Status != normalize.HealthDegraded {
			t.Fatalf("Claude health = %q, want degraded", record.Health.Status)
		}
	}
}

func TestOrchestratorMakesCancellationVisibleAndDurable(t *testing.T) {
	t.Parallel()
	findings := syntheticFindings(t)
	ctx, cancel := context.WithCancel(context.Background())
	discovery := cancelingDiscovery{findings: findings, cancel: cancel}
	db := openCollectionStore(t)
	result, err := (Orchestrator{Discovery: discovery, Store: db}).Scan(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan() error = %v, want context cancellation", err)
	}
	if !result.Canceled || !result.Persisted {
		t.Fatalf("Scan() result = %#v, want canceled durable state", result)
	}
	health, healthErr := db.AdapterHealthRecords(context.Background())
	if healthErr != nil {
		t.Fatal(healthErr)
	}
	if len(health) == 0 {
		t.Fatal("canceled scan did not persist source health")
	}
	for _, record := range health {
		if record.Health.Status != normalize.HealthError || !hasWarning(record.Health.Warnings, "collection_canceled") {
			t.Fatalf("canceled source health = %#v", record.Health)
		}
	}
}

type staticDiscovery []adapters.Finding

func (d staticDiscovery) Discover(context.Context) []adapters.Finding {
	return append([]adapters.Finding(nil), d...)
}

type cancelingDiscovery struct {
	findings []adapters.Finding
	cancel   context.CancelFunc
}

func (d cancelingDiscovery) Discover(context.Context) []adapters.Finding {
	d.cancel()
	return append([]adapters.Finding(nil), d.findings...)
}

func syntheticFindings(t *testing.T) []adapters.Finding {
	t.Helper()
	root := t.TempDir()
	roots := []adapters.RootSpec{
		testRoot(adapters.RootClaudeCLI, normalize.SourceClaude, normalize.SurfaceCLI, filepath.Join(root, "claude-cli")),
		testRoot(adapters.RootClaudeDesktopCode, normalize.SourceClaude, normalize.SurfaceDesktopCode, filepath.Join(root, "claude-desktop")),
		testRoot(adapters.RootCodexCLI, normalize.SourceCodex, normalize.SurfaceCLI, filepath.Join(root, "codex-cli")),
		testRoot(adapters.RootCodexDesktop, normalize.SourceCodex, normalize.SurfaceDesktop, filepath.Join(root, "codex-desktop")),
	}
	copyFixture(t, filepath.Join("claude-code", "duplicates.jsonl"), filepath.Join(roots[0].Path, "usage.jsonl"))
	copyFixture(t, filepath.Join("claude-desktop", "code", "overlap.jsonl"), filepath.Join(roots[1].Path, "usage.jsonl"))
	copyFixture(t, filepath.Join("codex-cli", "cumulative.jsonl"), filepath.Join(roots[2].Path, "rollout.jsonl"))
	copyFixture(t, filepath.Join("codex-desktop", "overlap.jsonl"), filepath.Join(roots[3].Path, "rollout.jsonl"))
	return (adapters.Discoverer{Roots: roots}).Discover(context.Background())
}

func testRoot(kind adapters.RootKind, source normalize.Source, surface normalize.Surface, path string) adapters.RootSpec {
	return adapters.RootSpec{
		Kind: kind, Source: source, Surface: surface, Path: path,
		Format: "synthetic-jsonl", Extensions: []string{".jsonl"},
		Capabilities: normalize.Capabilities{
			Model: normalize.CapabilityReported, InputUncached: normalize.CapabilityReported,
			InputCacheRead: normalize.CapabilityReported, InputCacheWrite: normalize.CapabilityReported,
			Output: normalize.CapabilityReported, ReasoningOutput: normalize.CapabilityUnsupported,
		},
	}
}

func copyFixture(t *testing.T, relative, destination string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "fixtures", relative))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func reverseFindings(findings []adapters.Finding) []adapters.Finding {
	result := append([]adapters.Finding(nil), findings...)
	for index := range result {
		result[index].Candidates = append([]adapters.Candidate(nil), result[index].Candidates...)
		for left, right := 0, len(result[index].Candidates)-1; left < right; left, right = left+1, right-1 {
			result[index].Candidates[left], result[index].Candidates[right] = result[index].Candidates[right], result[index].Candidates[left]
		}
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func openCollectionStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "overtok.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}
