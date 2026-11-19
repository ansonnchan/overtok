// Command tokenholic is the native Tokenholic CLI entry point.
package main

import (
	"os"

	"github.com/ansonnchan/tokenholic/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
