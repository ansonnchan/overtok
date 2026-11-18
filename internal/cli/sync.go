package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ansonnchan/overtok/internal/account"
	usageSync "github.com/ansonnchan/overtok/internal/sync"
)

const credentialRefreshWindow = 7 * 24 * time.Hour

func synchronizeLocal(ctx context.Context, home, codexHome, apiURL string, dryRun bool, output io.Writer) (err error) {
	database, application, err := openLocalApplication(ctx, home, codexHome)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), localCloseTimeout)
		defer cancel()
		if closeErr := database.Close(closeCtx); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if err := application.Refresh(ctx); err != nil {
		return err
	}
	credentialStore := account.NewKeyringStore(apiURL)
	session, err := credentialStore.Load()
	if err != nil {
		return err
	}
	if session.ExpiresAt.Before(time.Now().UTC().Add(credentialRefreshWindow)) {
		authClient, err := account.NewClient(apiURL, nil)
		if err != nil {
			return err
		}
		manager, err := account.NewManager(authClient, credentialStore)
		if err != nil {
			return err
		}
		if err := manager.Refresh(ctx); err != nil {
			return err
		}
		session, err = credentialStore.Load()
		if err != nil {
			return err
		}
	}
	client, err := usageSync.NewClient(apiURL, nil)
	if err != nil {
		return err
	}
	summary, err := client.Run(ctx, database, session.Credential, dryRun, output)
	if err != nil {
		return err
	}
	if !dryRun {
		fmt.Fprintf(output, "Synchronized %d buckets in %d batches.\n", summary.Acknowledged, summary.Batches)
	}
	return nil
}
