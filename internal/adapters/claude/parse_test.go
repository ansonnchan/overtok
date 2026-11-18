package claude

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
		{name: "CLI", file: "claude-code/basic.jsonl", surface: normalize.SurfaceCLI, wantRecords: 2},
		{name: "desktop code", file: "claude-desktop/code/basic.jsonl", surface: normalize.SurfaceDesktopCode, wantRecords: 1},
		{name: "desktop agent", file: "claude-desktop/agent/basic.jsonl", surface: normalize.SurfaceDesktopAgent, wantRecords: 1},
		{name: "desktop cowork", file: "claude-desktop/cowork/basic.jsonl", surface: normalize.SurfaceDesktopCowork, wantRecords: 1},
		{name: "malformed recovery", file: "claude-code/malformed-middle.jsonl", surface: normalize.SurfaceCLI, wantRecords: 2, wantWarnings: 1},
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
			result, err := Parse(file, tt.surface, "synthetic-v1")
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Observations) != tt.wantRecords || len(result.Warnings) != tt.wantWarnings {
				t.Fatalf("Parse() records/warnings = %d/%d, want %d/%d", len(result.Observations), len(result.Warnings), tt.wantRecords, tt.wantWarnings)
			}
		})
	}
}

func TestParseDefersPartialTail(t *testing.T) {
	t.Parallel()

	data := `{"type":"assistant","sessionId":"synthetic","requestId":"one","timestamp":"2026-01-02T03:04:05Z","message":{"model":"claude-synthetic","usage":{"input_tokens":1}}}`
	result, err := Parse(strings.NewReader(data), normalize.SurfaceCLI, "synthetic-v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 0 || len(result.Warnings) != 1 || result.Warnings[0].Code != "partial_tail" {
		t.Fatalf("Parse() = %#v", result)
	}
}
