package doctor

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/adapters/ccusage"
	"github.com/ansonnchan/overtok/internal/normalize"
)

type DiscoveryProbe interface {
	Discover(context.Context) []adapters.Finding
}

type ParserProbe interface {
	Check(context.Context) ParserStatus
}

type ParserStatus struct {
	Found      bool
	Compatible bool
	Issue      string
}

type Report struct {
	GOOS     string
	GOARCH   string
	Parser   ParserStatus
	Findings []adapters.Finding
}

func Diagnose(ctx context.Context, discovery DiscoveryProbe, parser ParserProbe) Report {
	report := Report{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Findings: discovery.Discover(ctx)}
	if parser != nil {
		report.Parser = parser.Check(ctx)
	}
	return report
}

func (r Report) ExitCode() int {
	if r.GOOS != "darwin" {
		return 1
	}
	if r.Parser.Issue != "" || r.Parser.Found && !r.Parser.Compatible {
		return 1
	}
	for _, finding := range r.Findings {
		switch finding.Status {
		case normalize.HealthPermissionDenied, normalize.HealthUnsupported, normalize.HealthDegraded, normalize.HealthError:
			return 1
		}
	}
	return 0
}

func (r Report) Render() string {
	var output strings.Builder
	fmt.Fprintln(&output, "Overtok doctor")
	fmt.Fprintf(&output, "Platform: %s/%s\n", r.GOOS, r.GOARCH)
	switch {
	case r.Parser.Compatible:
		fmt.Fprintf(&output, "ccusage: compatible (%s)\n", ccusage.PinnedVersion)
	case r.Parser.Issue == "timeout":
		fmt.Fprintln(&output, "ccusage: version check timed out")
	case r.Parser.Issue == "execution_failed":
		fmt.Fprintln(&output, "ccusage: found but could not execute")
	case r.Parser.Found:
		fmt.Fprintf(&output, "ccusage: unsupported (requires %s)\n", ccusage.PinnedVersion)
	default:
		fmt.Fprintf(&output, "ccusage: not found (optional pinned parser %s)\n", ccusage.PinnedVersion)
	}
	fmt.Fprintln(&output, "Sources:")

	findings := append([]adapters.Finding(nil), r.Findings...)
	sort.SliceStable(findings, func(i, j int) bool { return sourceOrder(findings[i].Kind) < sourceOrder(findings[j].Kind) })
	for _, finding := range findings {
		fmt.Fprintf(&output, "  %-29s %s", label(finding.Kind), statusText(finding))
		if finding.SharedRoot {
			fmt.Fprint(&output, "; shared records deduplicated")
		}
		if finding.Format != "" {
			fmt.Fprintf(&output, "; format %s", finding.Format)
		}
		if finding.Capabilities.Model != "" {
			fmt.Fprintf(&output, "; model %s; cache-write %s", finding.Capabilities.Model, finding.Capabilities.InputCacheWrite)
		}
		fmt.Fprintln(&output)
		for _, warning := range finding.Warnings {
			fmt.Fprintf(&output, "    warning: %s\n", warningText(warning.Code))
		}
	}
	if r.ExitCode() == 0 {
		fmt.Fprintln(&output, "Result: ready; missing optional sources are not errors")
	} else {
		fmt.Fprintln(&output, "Result: issues found; review the warnings above")
	}
	return output.String()
}

func statusText(finding adapters.Finding) string {
	switch finding.Status {
	case normalize.HealthDetected:
		return fmt.Sprintf("detected (%d candidate files)", finding.RecordCount)
	case normalize.HealthNotFound:
		return "not found"
	case normalize.HealthPermissionDenied:
		return "permission denied"
	case normalize.HealthUnsupported:
		return "unsupported layout"
	case normalize.HealthDegraded:
		return fmt.Sprintf("degraded (%d candidate files)", finding.RecordCount)
	default:
		return "error"
	}
}

func label(kind adapters.RootKind) string {
	switch kind {
	case adapters.RootClaudeCLI:
		return "Claude Code CLI"
	case adapters.RootClaudeDesktopCode:
		return "Claude Desktop Code"
	case adapters.RootClaudeDesktopAgent:
		return "Claude Desktop Agent"
	case adapters.RootClaudeDesktopCowork:
		return "Claude Desktop Cowork"
	case adapters.RootCodexCLI:
		return "Codex CLI"
	case adapters.RootCodexDesktop:
		return "Codex Desktop"
	case adapters.RootCodexShared:
		return "Codex CLI/Desktop shared"
	default:
		return "Unknown source"
	}
}

func sourceOrder(kind adapters.RootKind) int {
	switch kind {
	case adapters.RootClaudeCLI:
		return 0
	case adapters.RootClaudeDesktopCode:
		return 1
	case adapters.RootClaudeDesktopAgent:
		return 2
	case adapters.RootClaudeDesktopCowork:
		return 3
	case adapters.RootCodexCLI:
		return 5
	case adapters.RootCodexDesktop:
		return 6
	case adapters.RootCodexShared:
		return 4
	default:
		return 7
	}
}

func warningText(code string) string {
	switch code {
	case "source_root_unreadable", "source_entry_permission_denied":
		return "the source exists but cannot be read; check macOS file permissions"
	case "source_root_symlink":
		return "the source root is a symlink; configure the resolved data root explicitly"
	case "source_root_not_directory":
		return "the expected source root is not a directory"
	case "discovery_canceled":
		return "source discovery was canceled"
	case "no_candidate_records":
		return "the source root exists but has no candidate coding records"
	case "surface_attribution_unavailable":
		return "CLI and Desktop share this record; its originating surface is unavailable"
	default:
		return "source discovery could not complete"
	}
}
