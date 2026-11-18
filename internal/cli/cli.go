package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/doctor"
	"github.com/ansonnchan/overtok/internal/store"
)

const version = "dev"

// Run executes the native command without owning process termination.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, defaultDependencies())
}

type dependencies struct {
	homeDir  func() (string, error)
	getenv   func(string) string
	diagnose func(context.Context, string, string) doctor.Report
	input    io.Reader
	launch   func(context.Context, string, string, io.Reader, io.Writer, bool) error
	timeout  time.Duration
}

func defaultDependencies() dependencies {
	return dependencies{
		homeDir: os.UserHomeDir,
		getenv:  os.Getenv,
		input:   os.Stdin,
		launch:  launchLocalTUI,
		timeout: 5 * time.Second,
		diagnose: func(ctx context.Context, home, codexHome string) doctor.Report {
			discovery := adapters.Discoverer{Roots: adapters.DefaultMacRoots(home, codexHome)}
			path, err := store.DefaultPath()
			if err != nil {
				report := doctor.Diagnose(ctx, discovery, doctor.CCUsageProbe{}, nil)
				report.Database = doctor.DatabaseStatus{Location: "default application data", State: doctor.DatabaseError, SupportedVersion: store.SupportedSchemaVersion()}
				return report
			}
			return doctor.Diagnose(ctx, discovery, doctor.CCUsageProbe{}, doctor.LocalDatabaseProbe{Path: path, Default: true})
		},
	}
}

func run(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("overtok", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if flags.NArg() == 1 && flags.Arg(0) == "doctor" {
		return runDoctor(stdout, stderr, deps)
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unknown command %q\n", flags.Arg(0))
		return 2
	}

	return runTUI(stdout, stderr, deps)
}

func runTUI(stdout, stderr io.Writer, deps dependencies) int {
	home, err := deps.homeDir()
	if err != nil {
		fmt.Fprintln(stderr, "could not resolve the local home directory")
		return 1
	}
	codexHome := deps.getenv("CODEX_HOME")
	if codexHome != "" && !filepath.IsAbs(codexHome) {
		fmt.Fprintln(stderr, "CODEX_HOME must be an absolute directory")
		return 1
	}
	if deps.launch == nil {
		fmt.Fprintln(stderr, "could not start the local application")
		return 1
	}
	if err := deps.launch(context.Background(), home, codexHome, deps.input, stdout, deps.getenv("NO_COLOR") != ""); err != nil {
		// Runtime errors can wrap private local paths. Keep CLI output stable and
		// direct detailed investigation through the redacted doctor report.
		fmt.Fprintln(stderr, "could not start the local application; run `overtok doctor`")
		return 1
	}
	return 0
}

func runDoctor(stdout, stderr io.Writer, deps dependencies) int {
	home, err := deps.homeDir()
	if err != nil {
		fmt.Fprintln(stderr, "could not resolve the local home directory")
		return 1
	}
	codexHome := deps.getenv("CODEX_HOME")
	if codexHome != "" && !filepath.IsAbs(codexHome) {
		fmt.Fprintln(stderr, "CODEX_HOME must be an absolute directory")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), deps.timeout)
	defer cancel()
	report := deps.diagnose(ctx, home, codexHome)
	fmt.Fprint(stdout, report.Render())
	return report.ExitCode()
}
