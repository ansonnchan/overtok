package ccusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ansonnchan/overtok/internal/normalize"
)

type fakeExecutor struct {
	version []byte
	report  []byte
	stderr  []byte
	err     error
	calls   [][]string
}

func (f *fakeExecutor) Run(_ context.Context, _ string, args []string, _ int) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if reflect.DeepEqual(args, []string{"--version"}) {
		return f.version, f.stderr, f.err
	}
	return f.report, f.stderr, f.err
}

func TestClientCollectPinnedFixtures(t *testing.T) {
	t.Parallel()

	flags := []string{"daily", "--json", "--no-cost", "--offline", "--timezone", "UTC"}
	tests := []struct {
		name       string
		agent      Agent
		surface    normalize.Surface
		file       string
		wantArgs   []string
		resolution normalize.ModelResolution
	}{
		{name: "Claude", agent: AgentClaude, surface: normalize.SurfaceCLI, file: "claude-daily.json", wantArgs: flags, resolution: normalize.ModelReported},
		{name: "Codex", agent: AgentCodex, surface: normalize.SurfaceDesktop, file: "codex-daily.json", wantArgs: append([]string{"codex"}, flags...), resolution: normalize.ModelInferred},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "ccusage", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			executor := &fakeExecutor{version: []byte("ccusage " + PinnedVersion + "\n"), report: data}
			observations, err := (Client{Binary: "synthetic-ccusage", Executor: executor}).Collect(context.Background(), tt.agent, tt.surface)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) != 2 {
				t.Fatalf("Collect() observations = %d, want 2", len(observations))
			}
			if len(executor.calls) != 2 || !reflect.DeepEqual(executor.calls[1], tt.wantArgs) {
				t.Fatalf("executor calls = %#v, want report args %#v", executor.calls, tt.wantArgs)
			}
			for _, observation := range observations {
				if observation.ModelResolution != tt.resolution || observation.CountSemantics != normalize.CountCumulative || observation.Measurement != normalize.MeasurementDerived {
					t.Fatalf("observation metadata = %q/%q/%q", observation.ModelResolution, observation.CountSemantics, observation.Measurement)
				}
				if observation.Tokens.InputCacheWrite == nil {
					t.Fatal("cache-write tokens were not preserved")
				}
				if tt.agent == AgentCodex && observation.Tokens.ReasoningOutput == nil {
					t.Fatal("Codex reasoning tokens were not preserved")
				}
			}
		})
	}
}

func TestClientRejectsUnsupportedVersionBeforeReport(t *testing.T) {
	t.Parallel()

	executor := &fakeExecutor{version: []byte("ccusage 99.0.0")}
	_, err := (Client{Binary: "synthetic-ccusage", Executor: executor}).Collect(context.Background(), AgentClaude, normalize.SurfaceCLI)
	if err == nil || !strings.Contains(err.Error(), "unsupported ccusage version") {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("executor calls = %d, want 1", len(executor.calls))
	}
}

func TestClientRedactsExternalStderr(t *testing.T) {
	t.Parallel()

	executor := &fakeExecutor{stderr: []byte("/Users/private/source.jsonl"), err: errors.New("synthetic failure")}
	_, err := (Client{Binary: "synthetic-ccusage", Executor: executor}).Collect(context.Background(), AgentClaude, normalize.SurfaceCLI)
	if err == nil || strings.Contains(err.Error(), "/Users/") {
		t.Fatalf("Collect() error = %v", err)
	}
}

func TestClaudeSchemaRejectsContractViolations(t *testing.T) {
	t.Parallel()

	valid := `{"daily":[],"totals":{"inputTokens":0,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "missing totals", data: `{"daily":[]}`, want: "daily and totals are required"},
		{name: "missing total count", data: `{"daily":[],"totals":{"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "missing or null"},
		{name: "null total count", data: `{"daily":[],"totals":{"inputTokens":null,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "missing or null"},
		{name: "unknown field", data: `{"daily":[],"extra":1,"totals":{"inputTokens":0,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "unknown field"},
		{name: "duplicate key", data: `{"daily":[],"daily":[],"totals":{"inputTokens":0,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "duplicate JSON key"},
		{name: "trailing value", data: valid + `{}`, want: "trailing JSON"},
		{name: "fractional count", data: `{"daily":[],"totals":{"inputTokens":0.5,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "cannot unmarshal number"},
		{name: "negative count", data: `{"daily":[],"totals":{"inputTokens":-1,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "cannot unmarshal number"},
		{name: "overflow count", data: `{"daily":[],"totals":{"inputTokens":18446744073709551616,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":0}}`, want: "cannot unmarshal number"},
		{name: "inconsistent total", data: `{"daily":[],"totals":{"inputTokens":1,"outputTokens":0,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":1}}`, want: "declared totals"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeClaudeReport([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("decodeClaudeReport() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCodexSchemaRequiresReasoningAndModelTotals(t *testing.T) {
	t.Parallel()

	data := `{"daily":[{"date":"2026-01-01","inputTokens":1,"outputTokens":1,"cacheCreationTokens":0,"cacheReadTokens":0,"reasoningOutputTokens":0,"totalTokens":2,"models":{"gpt-synthetic":{"inputTokens":1,"outputTokens":1,"cacheCreationTokens":0,"cacheReadTokens":0,"totalTokens":2}}}],"totals":{"inputTokens":1,"outputTokens":1,"cacheCreationTokens":0,"cacheReadTokens":0,"reasoningOutputTokens":0,"totalTokens":2}}`
	if _, err := decodeCodexReport([]byte(data)); err == nil || !strings.Contains(err.Error(), "missing or null") {
		t.Fatalf("decodeCodexReport() error = %v", err)
	}
}

func TestLockManifestMatchesCode(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "ccusage.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLock(data); err != nil {
		t.Fatal(err)
	}
}

func TestCappedBufferRejectsOversizedOutput(t *testing.T) {
	t.Parallel()

	buffer := &cappedBuffer{limit: 3}
	if _, err := buffer.Write([]byte("four")); err != ErrOutputTooLarge {
		t.Fatalf("Write() error = %v, want %v", err, ErrOutputTooLarge)
	}
	if got := string(buffer.Bytes()); got != "fou" {
		t.Fatalf("buffer = %q, want %q", got, "fou")
	}
}

func TestStableIDIsDeterministic(t *testing.T) {
	t.Parallel()

	a := stableID(normalize.SourceClaude, normalize.SurfaceCLI, "2026-01-02", "synthetic")
	b := stableID(normalize.SourceClaude, normalize.SurfaceCLI, "2026-01-02", "synthetic")
	if a != b || strings.Contains(a, "/") {
		t.Fatalf("stable IDs = %q and %q", a, b)
	}
}
