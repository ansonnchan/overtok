package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ansonnchan/overtok/internal/adapters"
	"github.com/ansonnchan/overtok/internal/application"
	"github.com/ansonnchan/overtok/internal/collect"
	"github.com/ansonnchan/overtok/internal/modelcatalog"
	"github.com/ansonnchan/overtok/internal/store"
	"github.com/ansonnchan/overtok/internal/tui"
)

const localCloseTimeout = 5 * time.Second

func launchLocalTUI(ctx context.Context, home, codexHome string, input io.Reader, output io.Writer, noColor bool) (err error) {
	database, service, err := openLocalApplication(ctx, home, codexHome)
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
	return tui.Run(ctx, service, input, output, tui.Config{NoColor: noColor})
}

func openLocalApplication(ctx context.Context, home, codexHome string) (*store.DB, *application.Service, error) {
	path, err := store.DefaultPath()
	if err != nil {
		return nil, nil, err
	}
	database, err := store.Open(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	resolver, err := modelcatalog.NewResolver(modelcatalog.DefaultCatalog())
	if err != nil {
		_ = database.Close(context.Background())
		return nil, nil, fmt.Errorf("initialize model catalog: %w", err)
	}
	pricing, err := modelcatalog.DefaultPricingRegistry()
	if err != nil {
		_ = database.Close(context.Background())
		return nil, nil, fmt.Errorf("initialize pricing catalog: %w", err)
	}
	orchestrator := collect.Orchestrator{
		Discovery: adapters.Discoverer{Roots: adapters.DefaultMacRoots(home, codexHome)},
		Collector: collect.Collector{SourceVersion: "local-jsonl-v1"},
		Models:    resolver, Store: database, AdapterVersion: "native-v1",
	}
	service, err := application.NewService(database, orchestrator, pricing, time.Local, time.Now)
	if err != nil {
		_ = database.Close(context.Background())
		return nil, nil, err
	}
	return database, service, nil
}
