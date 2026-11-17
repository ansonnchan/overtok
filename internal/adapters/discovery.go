package adapters

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ansonnchan/overtok/internal/normalize"
)

type RootKind string

const (
	RootClaudeCLI           RootKind = "claude_cli"
	RootClaudeDesktopCode   RootKind = "claude_desktop_code"
	RootClaudeDesktopAgent  RootKind = "claude_desktop_agent"
	RootClaudeDesktopCowork RootKind = "claude_desktop_cowork"
	RootCodexCLI            RootKind = "codex_cli"
	RootCodexDesktop        RootKind = "codex_desktop"
	RootCodexShared         RootKind = "codex_shared"
)

type RootSpec struct {
	Kind         RootKind
	Source       normalize.Source
	Surface      normalize.Surface
	Path         string
	Format       string
	Extensions   []string
	Match        func(string) bool
	Capabilities normalize.Capabilities
}

type Candidate struct {
	Source       normalize.Source
	Surface      normalize.Surface
	Kind         RootKind
	Path         string
	RelativePath string
}

type Finding struct {
	Kind         RootKind
	Source       normalize.Source
	Surface      normalize.Surface
	Status       normalize.HealthStatus
	Format       string
	RecordCount  uint64
	Candidates   []Candidate
	Warnings     []normalize.Warning
	SharedRoot   bool
	Capabilities normalize.Capabilities
}

type Discoverer struct {
	Roots []RootSpec
}

func (d Discoverer) Discover(ctx context.Context) []Finding {
	findings := make([]Finding, 0, len(d.Roots))
	for _, root := range d.Roots {
		findings = append(findings, discoverRoot(ctx, root))
	}
	markSharedCandidates(findings)
	return findings
}

func discoverRoot(ctx context.Context, root RootSpec) Finding {
	finding := Finding{
		Kind: root.Kind, Source: root.Source, Surface: root.Surface, Format: root.Format,
		Capabilities: root.Capabilities,
	}
	info, err := os.Lstat(root.Path)
	if err != nil {
		finding.Status = statusForError(err)
		if finding.Status != normalize.HealthNotFound {
			finding.Warnings = []normalize.Warning{{Code: "source_root_unreadable"}}
		}
		return finding
	}
	if info.Mode()&os.ModeSymlink != 0 {
		finding.Status = normalize.HealthUnsupported
		finding.Warnings = []normalize.Warning{{Code: "source_root_symlink"}}
		return finding
	}
	if !info.IsDir() {
		finding.Status = normalize.HealthUnsupported
		finding.Warnings = []normalize.Warning{{Code: "source_root_not_directory"}}
		return finding
	}

	finding.Status = normalize.HealthDetected
	err = filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				finding.Status = normalize.HealthPermissionDenied
				finding.Warnings = append(finding.Warnings, normalize.Warning{Code: "source_entry_permission_denied"})
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if path != root.Path && entry.Type()&os.ModeSymlink != 0 {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root.Path, path)
		if err != nil {
			return fmt.Errorf("derive source-relative record identity: %w", err)
		}
		if !matchesExtension(path, root.Extensions) || (root.Match != nil && !root.Match(relative)) {
			return nil
		}
		finding.Candidates = append(finding.Candidates, Candidate{
			Source: root.Source, Surface: root.Surface, Kind: root.Kind,
			Path: path, RelativePath: relative,
		})
		finding.RecordCount++
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finding.Status = normalize.HealthError
			finding.Warnings = append(finding.Warnings, normalize.Warning{Code: "discovery_canceled"})
			return finding
		}
		finding.Status = statusForError(err)
		finding.Warnings = append(finding.Warnings, normalize.Warning{Code: "source_scan_failed"})
	}
	if finding.Status == normalize.HealthDetected && finding.RecordCount == 0 {
		finding.Status = normalize.HealthDegraded
		finding.Warnings = append(finding.Warnings, normalize.Warning{Code: "no_candidate_records"})
	}
	return finding
}

func markSharedCandidates(findings []Finding) {
	owners := make(map[string][]int)
	for index, finding := range findings {
		for _, candidate := range finding.Candidates {
			owners[filepath.Clean(candidate.Path)] = append(owners[filepath.Clean(candidate.Path)], index)
		}
	}
	for _, indexes := range owners {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			findings[index].SharedRoot = true
			if findings[index].Source == normalize.SourceCodex && !hasWarningCode(findings[index].Warnings, "surface_attribution_unavailable") {
				findings[index].Warnings = append(findings[index].Warnings, normalize.Warning{Code: "surface_attribution_unavailable"})
			}
		}
	}
}

func hasWarningCode(warnings []normalize.Warning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func DefaultMacRoots(home, codexHome string) []RootSpec {
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	claudeDesktop := filepath.Join(home, "Library", "Application Support", "Claude")
	defaultCodexHome := filepath.Join(home, ".codex")

	roots := []RootSpec{
		{
			Kind: RootClaudeCLI, Source: normalize.SourceClaude, Surface: normalize.SurfaceCLI,
			Path: filepath.Join(home, ".claude", "projects"), Format: "claude-jsonl-v1", Extensions: []string{".jsonl"},
			Capabilities: claudeCapabilities(),
		},
		{
			Kind: RootClaudeDesktopCode, Source: normalize.SourceClaude, Surface: normalize.SurfaceDesktopCode,
			Path: filepath.Join(claudeDesktop, "claude-code-sessions"), Format: "claude-desktop-code", Extensions: []string{".json", ".jsonl"},
			Capabilities: claudeCapabilities(),
		},
		{
			Kind: RootClaudeDesktopAgent, Source: normalize.SourceClaude, Surface: normalize.SurfaceDesktopAgent,
			Path: filepath.Join(claudeDesktop, "local-agent-mode-sessions"), Format: "claude-local-agent-jsonl", Extensions: []string{".jsonl"},
			Match: func(relative string) bool { return hasPathSegment(relative, "rpm") }, Capabilities: claudeCapabilities(),
		},
		{
			Kind: RootClaudeDesktopCowork, Source: normalize.SourceClaude, Surface: normalize.SurfaceDesktopCowork,
			Path: filepath.Join(claudeDesktop, "local-agent-mode-sessions"), Format: "claude-cowork-audit-jsonl", Extensions: []string{".jsonl"},
			Match: func(relative string) bool {
				return filepath.Base(relative) == "audit.jsonl" && !hasPathSegment(relative, "rpm")
			}, Capabilities: claudeCapabilities(),
		},
	}
	if filepath.Clean(codexHome) == filepath.Clean(defaultCodexHome) {
		return append(roots, RootSpec{
			Kind: RootCodexShared, Source: normalize.SourceCodex, Surface: normalize.SurfaceSharedLocal,
			Path: filepath.Join(defaultCodexHome, "sessions"), Format: "codex-rollout-jsonl", Extensions: []string{".jsonl"},
			Capabilities: codexCapabilities(),
		})
	}
	return append(roots,
		RootSpec{
			Kind: RootCodexCLI, Source: normalize.SourceCodex, Surface: normalize.SurfaceCLI,
			Path: filepath.Join(codexHome, "sessions"), Format: "codex-rollout-jsonl", Extensions: []string{".jsonl"},
			Capabilities: codexCapabilities(),
		},
		RootSpec{
			Kind: RootCodexDesktop, Source: normalize.SourceCodex, Surface: normalize.SurfaceDesktop,
			Path: filepath.Join(defaultCodexHome, "sessions"), Format: "codex-rollout-jsonl", Extensions: []string{".jsonl"},
			Capabilities: codexCapabilities(),
		},
	)
}

func matchesExtension(path string, allowed []string) bool {
	for _, extension := range allowed {
		if strings.EqualFold(filepath.Ext(path), extension) {
			return true
		}
	}
	return false
}

func hasPathSegment(path, segment string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func statusForError(err error) normalize.HealthStatus {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return normalize.HealthNotFound
	case errors.Is(err, fs.ErrPermission):
		return normalize.HealthPermissionDenied
	default:
		return normalize.HealthError
	}
}

func claudeCapabilities() normalize.Capabilities {
	return normalize.Capabilities{
		Model: normalize.CapabilityReported, InputUncached: normalize.CapabilityReported,
		InputCacheRead: normalize.CapabilityReported, InputCacheWrite: normalize.CapabilityReported,
		Output: normalize.CapabilityReported, ReasoningOutput: normalize.CapabilityUnsupported,
	}
}

func codexCapabilities() normalize.Capabilities {
	return normalize.Capabilities{
		Model: normalize.CapabilityInferred, InputUncached: normalize.CapabilityDerived,
		InputCacheRead: normalize.CapabilityReported, InputCacheWrite: normalize.CapabilityUnsupported,
		Output: normalize.CapabilityReported, ReasoningOutput: normalize.CapabilityReported,
	}
}
