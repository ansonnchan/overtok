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
	"github.com/ansonnchan/overtok/internal/multiplayer"
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
		launch: func(ctx context.Context, home, codexHome, apiURL string, gotInput io.Reader, _ io.Writer, noColor bool) error {
			if ctx == nil || home != "/synthetic/home" || codexHome != "/synthetic/codex" || apiURL != "" || gotInput != input || !noColor {
				t.Fatalf("launch inputs = %v %q %q %q %v %v", ctx, home, codexHome, apiURL, gotInput, noColor)
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
		launch: func(context.Context, string, string, string, io.Reader, io.Writer, bool) error {
			return errors.New("private/path/state.db")
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, deps); code != 1 || !strings.Contains(stderr.String(), "overtok doctor") || strings.Contains(stderr.String(), "private/path") {
		t.Fatalf("root code/stderr = %d/%q", code, stderr.String())
	}
}

func TestLocalTimezoneResolvesLoadableLocation(t *testing.T) {
	zone := localTimezone()
	if zone == "" || zone == "Local" {
		t.Fatalf("localTimezone() = %q", zone)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		t.Fatalf("localTimezone() = %q: %v", zone, err)
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

func TestAccountCommandsUseConfiguredAPIAndRedactFailures(t *testing.T) {
	t.Parallel()
	manager := &fakeAccountManager{}
	deps := dependencies{
		getenv: func(name string) string {
			if name == "OVERTOK_API_URL" {
				return "https://api.example.test"
			}
			return ""
		},
		account: func(apiURL string) (accountManager, error) {
			if apiURL != "https://api.example.test" {
				t.Fatalf("api URL = %q", apiURL)
			}
			return manager, nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"login"}, &stdout, &stderr, deps); code != 0 || manager.loginVersion != version {
		t.Fatalf("login = %d/%q/%q manager=%#v", code, stdout.String(), stderr.String(), manager)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"logout"}, &stdout, &stderr, deps); code != 0 || !manager.loggedOut || !strings.Contains(stdout.String(), "revoked") {
		t.Fatalf("logout = %d/%q/%q manager=%#v", code, stdout.String(), stderr.String(), manager)
	}
	manager.loginErr = errors.New("secret provider token at /private/path")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"login"}, &stdout, &stderr, deps); code != 1 || strings.Contains(stderr.String(), "provider token") || strings.Contains(stderr.String(), "/private/path") {
		t.Fatalf("failed login = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestAccountCommandRequiresAPIURL(t *testing.T) {
	t.Parallel()
	deps := dependencies{getenv: func(string) string { return "" }}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"login"}, &stdout, &stderr, deps); code != 1 || !strings.Contains(stderr.String(), "OVERTOK_API_URL") {
		t.Fatalf("login = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestSyncCommandParsesDryRunAndRedactsFailure(t *testing.T) {
	t.Parallel()
	var gotDryRun bool
	deps := dependencies{
		homeDir: func() (string, error) { return "/synthetic/home", nil },
		getenv: func(name string) string {
			if name == "OVERTOK_API_URL" {
				return "https://api.example.test"
			}
			return ""
		},
		sync: func(_ context.Context, home, codexHome, apiURL string, dryRun bool, output io.Writer) error {
			if home != "/synthetic/home" || codexHome != "" || apiURL != "https://api.example.test" {
				t.Fatalf("sync inputs = %q/%q/%q", home, codexHome, apiURL)
			}
			gotDryRun = dryRun
			_, _ = io.WriteString(output, "{\"schema_version\":1}\n")
			return nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"sync", "--dry-run"}, &stdout, &stderr, deps); code != 0 || !gotDryRun || !strings.Contains(stdout.String(), "schema_version") {
		t.Fatalf("sync dry run = %d/%q/%q dry=%v", code, stdout.String(), stderr.String(), gotDryRun)
	}
	deps.sync = func(context.Context, string, string, string, bool, io.Writer) error {
		return errors.New("private/path and credential")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"sync"}, &stdout, &stderr, deps); code != 1 || strings.Contains(stderr.String(), "private/path") || strings.Contains(stderr.String(), "credential") {
		t.Fatalf("failed sync = %d/%q/%q", code, stdout.String(), stderr.String())
	}
}

func TestGroupCommandsUseConfiguredMultiplayerBoundary(t *testing.T) {
	manager := &fakeGroupManager{groups: []multiplayer.Group{{ID: "group-id", Name: "Quiet group", Role: "owner", Timezone: "UTC"}}}
	deps := dependencies{
		getenv: func(name string) string {
			if name == "OVERTOK_API_URL" {
				return "https://api.example.test"
			}
			return ""
		},
		groups: func(apiURL string) (groupManager, error) {
			if apiURL != "https://api.example.test" {
				t.Fatalf("api URL = %q", apiURL)
			}
			return manager, nil
		},
	}
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"group", "list"}, want: "Quiet group · owner · UTC"},
		{args: []string{"group", "create", "New group"}, want: "Created private group New group"},
		{args: []string{"group", "join", "ovt_synthetic"}, want: "Joined private group Joined"},
		{args: []string{"group", "invite", "--expires", "1h", "--uses", "2", "Quiet group"}, want: "Invite code: ovt_invite"},
		{args: []string{"group", "leave", "Quiet group"}, want: "Left private group Quiet group"},
	}
	for _, test := range tests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := run(test.args, &stdout, &stderr, deps); code != 0 || !strings.Contains(stdout.String(), test.want) || stderr.Len() != 0 {
			t.Fatalf("run(%v) = %d/%q/%q", test.args, code, stdout.String(), stderr.String())
		}
	}
	if manager.created != "New group" || manager.joined != "ovt_synthetic" || manager.inviteUses != 2 || manager.left != "Quiet group" {
		t.Fatalf("group manager = %#v", manager)
	}
}

func TestGroupCommandRequiresAPIAndRedactsFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"group", "list"}, &stdout, &stderr, dependencies{getenv: func(string) string { return "" }}); code != 1 || !strings.Contains(stderr.String(), "OVERTOK_API_URL") {
		t.Fatalf("missing API = %d/%q", code, stderr.String())
	}
	manager := &fakeGroupManager{err: errors.New("credential at /private/path")}
	deps := dependencies{
		getenv: func(string) string { return "https://api.example.test" },
		groups: func(string) (groupManager, error) { return manager, nil },
	}
	stderr.Reset()
	if code := run([]string{"group", "list"}, &stdout, &stderr, deps); code != 1 || strings.Contains(stderr.String(), "credential") || strings.Contains(stderr.String(), "/private/path") {
		t.Fatalf("failed group command = %d/%q", code, stderr.String())
	}
}

type fakeAccountManager struct {
	loginVersion string
	loginErr     error
	loggedOut    bool
}

type fakeGroupManager struct {
	groups     []multiplayer.Group
	created    string
	joined     string
	inviteUses int
	left       string
	err        error
}

func (f *fakeGroupManager) List(context.Context) ([]multiplayer.Group, error) {
	return f.groups, f.err
}
func (f *fakeGroupManager) Create(_ context.Context, name string) (multiplayer.Group, error) {
	f.created = name
	return multiplayer.Group{Name: name, Timezone: "UTC"}, f.err
}
func (f *fakeGroupManager) Join(_ context.Context, code string) (multiplayer.Group, error) {
	f.joined = code
	return multiplayer.Group{Name: "Joined"}, f.err
}
func (f *fakeGroupManager) Invite(_ context.Context, _ string, lifetime time.Duration, uses int) (multiplayer.Invite, error) {
	f.inviteUses = uses
	return multiplayer.Invite{Code: "ovt_invite", ExpiresAt: time.Now().UTC().Add(lifetime), MaxUses: uses}, f.err
}
func (f *fakeGroupManager) Leave(_ context.Context, group string) error {
	f.left = group
	return f.err
}

func (f *fakeAccountManager) Login(_ context.Context, version string, _ io.Writer) error {
	f.loginVersion = version
	return f.loginErr
}

func (f *fakeAccountManager) Logout(context.Context) error {
	f.loggedOut = true
	return nil
}
