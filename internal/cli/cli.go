package cli

import (
	"flag"
	"fmt"
	"io"
)

const version = "dev"

// Run executes the native command without owning process termination.
func Run(args []string, stdout, stderr io.Writer) int {
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
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unknown command %q\n", flags.Arg(0))
		return 2
	}

	fmt.Fprintln(stdout, "Overtok local collection is not configured yet.")
	fmt.Fprintln(stdout, "Run `overtok --version` to inspect this build.")
	return 0
}
