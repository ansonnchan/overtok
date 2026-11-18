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

	serverauth "github.com/ansonnchan/overtok/server/auth"
	"github.com/ansonnchan/overtok/server/httpapi"
	"github.com/ansonnchan/overtok/server/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "overtok api:", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	githubClientID := strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID"))
	stateKey, err := decodeStateKey(os.Getenv("OVERTOK_AUTH_STATE_KEY"))
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
	handler, err := httpapi.New(authentication)
	if err != nil {
		return err
	}
	address := strings.TrimSpace(os.Getenv("OVERTOK_LISTEN_ADDR"))
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
		return nil, errors.New("OVERTOK_AUTH_STATE_KEY must be base64 for exactly 32 bytes")
	}
	return key, nil
}
