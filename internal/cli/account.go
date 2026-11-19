package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"
)

func runAccountCommand(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) == 0 || args[0] != "delete" {
		fmt.Fprintln(stderr, "account requires delete")
		return 2
	}
	flags := flag.NewFlagSet("overtok account delete", flag.ContinueOnError)
	flags.SetOutput(stderr)
	confirmation := flags.String("confirm", "", "type DELETE to confirm permanent hosted-data deletion")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *confirmation != "DELETE" {
		fmt.Fprintln(stderr, "account delete requires --confirm DELETE")
		return 2
	}
	manager, ok := configuredAccountManager(stderr, deps)
	if !ok {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.DeleteAccount(ctx); err != nil {
		fmt.Fprintln(stderr, "could not delete the hosted account; no local credential was removed")
		return 1
	}
	fmt.Fprintln(stdout, "Deleted the hosted account and local session. Local usage data was not changed.")
	return 0
}
