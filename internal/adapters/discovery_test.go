package adapters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ansonnchan/tokenholic/internal/normalize"
)

func TestDefaultMacRootsDiscoverSupportedSurfaces(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	paths := map[RootKind]string{
		RootClaudeCLI:           filepath.Join(home, ".claude", "projects", "synthetic", "session.jsonl"),
		RootClaudeDesktopCode:   filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions", "synthetic", "session.jsonl"),
		RootClaudeDesktopAgent:  filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "synthetic", "rpm", "agent.jsonl"),
		RootClaudeDesktopCowork: filepath.Join(home, "Library", "Application Support", "Claude", "local-agent-mode-sessions", "synthetic", "local_synthetic", "audit.jsonl"),
		RootCodexShared:         filepath.Join(home, ".codex", "sessions", "2026", "01", "02", "rollout-synthetic.jsonl"),
	}
	for _, path := range paths {
		writeSyntheticFile(t, path)
	}
	ordinaryChat := filepath.Join(home, "Library", "Application Support", "Claude", "conversations", "ordinary-chat.jsonl")
	writeSyntheticFile(t, ordinaryChat)

	findings := (Discoverer{Roots: DefaultMacRoots(home, "")}).Discover(context.Background())
	if len(findings) != 5 {
		t.Fatalf("Discover() findings = %d, want 5", len(findings))
	}
	for _, finding := range findings {
		if finding.Status != normalize.HealthDetected {
			t.Errorf("%s status = %q, want detected", finding.Kind, finding.Status)
		}
		if finding.RecordCount != 1 {
			t.Errorf("%s count = %d, want 1", finding.Kind, finding.RecordCount)
		}
		for _, candidate := range finding.Candidates {
			if candidate.Path == ordinaryChat {
				t.Errorf("%s discovered ordinary desktop chat", finding.Kind)
			}
		}
	}
	if findings[2].SharedRoot || findings[3].SharedRoot {
		t.Fatal("disjoint Agent/Cowork candidates were incorrectly marked as shared")
	}
}

func TestDistinctCodexHomesRemainSeparate(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "codex")
	roots := DefaultMacRoots(home, custom)
	if len(roots) != 6 || roots[4].Kind != RootCodexCLI || roots[5].Kind != RootCodexDesktop {
		t.Fatalf("distinct Codex roots = %#v", roots)
	}
}

func TestDiscoverMissingAndUnsupportedRoots(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	fileRoot := filepath.Join(home, "not-a-directory")
	writeSyntheticFile(t, fileRoot)
	roots := []RootSpec{
		{Kind: RootClaudeCLI, Source: normalize.SourceClaude, Surface: normalize.SurfaceCLI, Path: filepath.Join(home, "missing"), Extensions: []string{".jsonl"}},
		{Kind: RootCodexCLI, Source: normalize.SourceCodex, Surface: normalize.SurfaceCLI, Path: fileRoot, Extensions: []string{".jsonl"}},
	}
	findings := (Discoverer{Roots: roots}).Discover(context.Background())
	if findings[0].Status != normalize.HealthNotFound {
		t.Errorf("missing status = %q", findings[0].Status)
	}
	if findings[1].Status != normalize.HealthUnsupported {
		t.Errorf("file status = %q", findings[1].Status)
	}
}

func TestDiscoverEmptyRootIsDegraded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	finding := (Discoverer{Roots: []RootSpec{{
		Kind: RootClaudeCLI, Source: normalize.SourceClaude, Surface: normalize.SurfaceCLI,
		Path: root, Extensions: []string{".jsonl"},
	}}}).Discover(context.Background())[0]
	if finding.Status != normalize.HealthDegraded || !hasWarningCode(finding.Warnings, "no_candidate_records") {
		t.Fatalf("empty root finding = %#v", finding)
	}
}

func TestDiscoverCancellationIsVisible(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSyntheticFile(t, filepath.Join(root, "record.jsonl"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finding := (Discoverer{Roots: []RootSpec{{
		Kind: RootClaudeCLI, Source: normalize.SourceClaude, Surface: normalize.SurfaceCLI,
		Path: root, Extensions: []string{".jsonl"},
	}}}).Discover(ctx)[0]
	if finding.Status != normalize.HealthError || len(finding.Warnings) == 0 || finding.Warnings[0].Code != "discovery_canceled" {
		t.Fatalf("canceled finding = %#v", finding)
	}
}

func writeSyntheticFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
