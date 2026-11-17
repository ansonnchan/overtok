package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ansonnchan/overtok/internal/normalize"
)

func TestParseCompatibilityFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		file         string
		surface      normalize.Surface
		wantRecords  int
		wantWarnings int
	}{
		{name: "CLI cumulative", file: "codex-cli/cumulative.jsonl", surface: normalize.SurfaceCLI, wantRecords: 3},
		{name: "CLI reset", file: "codex-cli/counter-reset.jsonl", surface: normalize.SurfaceCLI, wantRecords: 2},
		{name: "desktop", file: "codex-desktop/basic.jsonl", surface: normalize.SurfaceDesktop, wantRecords: 1},
		{name: "malformed recovery", file: "codex-cli/malformed-middle.jsonl", surface: normalize.SurfaceCLI, wantRecords: 1, wantWarnings: 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file, err := os.Open(filepath.Join("..", "..", "..", "fixtures", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			result, err := Parse(file, tt.surface, "synthetic-v1", "synthetic-file")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Observations) != tt.wantRecords || len(result.Warnings) != tt.wantWarnings {
				t.Fatalf("Parse() records/warnings = %d/%d, want %d/%d", len(result.Observations), len(result.Warnings), tt.wantRecords, tt.wantWarnings)
			}
			for _, observation := range result.Observations {
				if observation.CountSemantics != normalize.CountCumulative {
					t.Fatalf("count semantics = %q", observation.CountSemantics)
				}
			}
		})
	}
}

func TestParseRetainsTokensWithUnknownModel(t *testing.T) {
	t.Parallel()

	data := `{"timestamp":"2026-01-04T03:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":3}}}}` + "\n"
	result, err := Parse(strings.NewReader(data), normalize.SurfaceCLI, "synthetic-v1", "synthetic-file")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 || result.Observations[0].ModelResolution != normalize.ModelUnknown || result.Observations[0].Tokens.Output == nil {
		t.Fatalf("Parse() = %#v", result)
	}
}
