package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	serveraccount "github.com/ansonnchan/tokenholic/server/account"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
	"github.com/ansonnchan/tokenholic/server/httpapi"
	serverleaderboard "github.com/ansonnchan/tokenholic/server/leaderboard"
	"github.com/ansonnchan/tokenholic/server/postgres"
	serversync "github.com/ansonnchan/tokenholic/server/sync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tokenholic api:", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	githubClientID := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	stateKey, err := decodeStateKey(os.Getenv("TOKENHOLIC_AUTH_STATE_KEY"))
	if err != nil {
		return err
	}
	if githubClientID == "" {
		return errors.New("GITHUB_CLIENT_ID is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	pricing, err := modelcatalog.DefaultPricingRegistry()
	if err != nil {
		return err
	}
	if err := postgres.EnsurePricingRevision(ctx, pool, pricing); err != nil {
		return err
	}
	repository, err := postgres.NewAuthRepository(pool)
	if err != nil {
		return err
	}
	github, err := serverauth.NewGitHubClient(&http.Client{Timeout: 15 * time.Second}, githubClientID)
	if err != nil {
		return err
	}
	authentication, err := serverauth.NewService(github, repository, stateKey)
	if err != nil {
		return err
	}
	usageRepository, err := postgres.NewUsageRepository(pool)
	if err != nil {
		return err
	}
	synchronization, err := serversync.NewService(authentication, usageRepository)
	if err != nil {
		return err
	}
	accountRepository, err := postgres.NewAccountRepository(pool)
	if err != nil {
		return err
	}
	accountUsage, err := serveraccount.NewService(authentication, accountRepository)
	if err != nil {
		return err
	}
	groupRepository, err := postgres.NewGroupRepository(pool)
	if err != nil {
		return err
	}
	groups, err := servergroup.NewService(authentication, groupRepository)
	if err != nil {
		return err
	}
	leaderboardRepository, err := postgres.NewLeaderboardRepository(pool)
	if err != nil {
		return err
	}
	leaderboards, err := serverleaderboard.NewService(authentication, leaderboardRepository)
	if err != nil {
		return err
	}
	handler, err := httpapi.New(authentication, synchronization, accountUsage, groups, leaderboards)
	if err != nil {
		return err
	}
	address := strings.TrimSpace(os.Getenv("TOKENHOLIC_LISTEN_ADDR"))
	if address == "" {
		address = "127.0.0.1:8080"
	}
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		result <- server.ListenAndServe()
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP API: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP API: %w", err)
		}
		return nil
	}
}

func decodeStateKey(encoded string) ([]byte, error) {
	value := strings.TrimSpace(encoded)
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("TOKENHOLIC_AUTH_STATE_KEY must be base64 for exactly 32 bytes")
	}
	return key, nil
}
