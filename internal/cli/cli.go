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
	timeout  time.Duration
}

func defaultDependencies() dependencies {
	return dependencies{
		homeDir: os.UserHomeDir,
		getenv:  os.Getenv,
		timeout: 5 * time.Second,
		diagnose: func(ctx context.Context, home, codexHome string) doctor.Report {
			discovery := adapters.Discoverer{Roots: adapters.DefaultMacRoots(home, codexHome)}
			return doctor.Diagnose(ctx, discovery, doctor.CCUsageProbe{})
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

	fmt.Fprintln(stdout, "Overtok local collection is not configured yet.")
	fmt.Fprintln(stdout, "Run `overtok --version` to inspect this build.")
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
