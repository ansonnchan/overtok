package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/doctor"
	"github.com/ansonnchan/overtok/internal/normalize"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"--version"}, wantCode: 0, wantStdout: "dev\n"},
		{name: "unknown", args: []string{"collect"}, wantCode: 2, wantStderr: "unknown command"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			got := Run(tt.args, &stdout, &stderr)
			if got != tt.wantCode {
				t.Fatalf("Run() code = %d, want %d", got, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestRootCommandLaunchesLocalTUIWithEnvironment(t *testing.T) {
	t.Parallel()
	input := strings.NewReader("q")
	deps := dependencies{
		homeDir: func() (string, error) { return "/synthetic/home", nil },
		getenv: func(name string) string {
			switch name {
			case "CODEX_HOME":
				return "/synthetic/codex"
			case "NO_COLOR":
				return "1"
			default:
				return ""
			}
		},
		input: input,
		launch: func(ctx context.Context, home, codexHome string, gotInput io.Reader, _ io.Writer, noColor bool) error {
			if ctx == nil || home != "/synthetic/home" || codexHome != "/synthetic/codex" || gotInput != input || !noColor {
				t.Fatalf("launch inputs = %v %q %q %v %v", ctx, home, codexHome, gotInput, noColor)
			}
			return nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, deps); code != 0 || stderr.Len() != 0 {
		t.Fatalf("root code/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestRootCommandRedactsStartupFailure(t *testing.T) {
	t.Parallel()
	deps := dependencies{
		homeDir: func() (string, error) { return "/synthetic/home", nil },
		getenv:  func(string) string { return "" },
		input:   strings.NewReader("q"),
		launch: func(context.Context, string, string, io.Reader, io.Writer, bool) error {
			return errors.New("private/path/state.db")
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, deps); code != 1 || !strings.Contains(stderr.String(), "overtok doctor") || strings.Contains(stderr.String(), "private/path") {
		t.Fatalf("root code/stderr = %d/%q", code, stderr.String())
	}
}

func TestDoctorCommandUsesBoundedInjectedDiagnostics(t *testing.T) {
	t.Parallel()

	privateHome := "/synthetic/private-home"
	deps := dependencies{
		homeDir: func() (string, error) { return privateHome, nil },
		getenv:  func(string) string { return "" },
		timeout: time.Second,
		diagnose: func(ctx context.Context, home, codexHome string) doctor.Report {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("doctor context has no deadline")
			}
			if home != privateHome || codexHome != "" {
				t.Fatalf("diagnose inputs = %q/%q", home, codexHome)
			}
			return doctor.Report{GOOS: "darwin", GOARCH: "arm64", Findings: []adapters.Finding{{
				Kind: adapters.RootClaudeCLI, Status: normalize.HealthDetected, RecordCount: 1,
			}}}
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr, deps); code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Claude Code CLI") || strings.Contains(stdout.String(), privateHome) {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}

func TestDoctorCommandRejectsRelativeCodexHome(t *testing.T) {
	t.Parallel()

	deps := dependencies{
		homeDir: func() (string, error) { return "/synthetic/home", nil },
		getenv:  func(string) string { return "relative" },
		timeout: time.Second,
		diagnose: func(context.Context, string, string) doctor.Report {
			t.Fatal("diagnose should not run")
			return doctor.Report{}
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr, deps); code != 1 || !strings.Contains(stderr.String(), "absolute") {
		t.Fatalf("doctor code/stdout/stderr = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestDoctorCommandHandlesHomeResolutionFailure(t *testing.T) {
	t.Parallel()

	deps := dependencies{
		homeDir: func() (string, error) { return "", errors.New("synthetic") },
		getenv:  func(string) string { return "" }, timeout: time.Second,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr, deps); code != 1 || !strings.Contains(stderr.String(), "resolve") {
		t.Fatalf("doctor code/stderr = %d/%q", code, stderr.String())
	}
}
