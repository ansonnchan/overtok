package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ansonnchan/tokenholic/internal/account"
	"github.com/ansonnchan/tokenholic/internal/autosync"
	usageSync "github.com/ansonnchan/tokenholic/internal/sync"
)

const credentialRefreshWindow = 7 * 24 * time.Hour

func synchronizeLocal(ctx context.Context, home, codexHome, apiURL string, dryRun bool, output io.Writer) (err error) {
	lock, err := autosync.AcquireDefault()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := lock.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
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
	if dryRun {
		_, err := usageSync.DryRun(ctx, database, output)
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
	summary, err := client.Run(ctx, database, session.Credential)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Synchronized %d buckets in %d batches.\n", summary.Acknowledged, summary.Batches)
	return nil
}
