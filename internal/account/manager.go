package account

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Store persists session secrets in an operating-system credential vault.
type Store interface {
	Load() (Session, error)
	Save(Session) error
	Delete() error
}

type authClient interface {
	Start(context.Context, string) (DeviceStart, error)
	Poll(context.Context, string) (PollResult, error)
	Refresh(context.Context, string) (Session, error)
	Logout(context.Context, string) error
	DeleteAccount(context.Context, string) error
}

// Manager coordinates interactive login and local credential storage.
type Manager struct {
	client authClient
	store  Store
	sleep  func(context.Context, time.Duration) error
}

// NewManager constructs a native account manager.
func NewManager(client authClient, store Store) (*Manager, error) {
	if client == nil || store == nil {
		return nil, errors.New("initialize account manager: client and credential store are required")
	}
	return &Manager{client: client, store: store, sleep: sleepContext}, nil
}

// Login completes GitHub device authorization and stores only the resulting
// tokenholic device session.
func (m *Manager) Login(ctx context.Context, clientVersion string, output io.Writer) error {
	started, err := m.client.Start(ctx, clientVersion)
	if err != nil {
		return fmt.Errorf("start login: %w", err)
	}
	fmt.Fprintf(output, "Open %s and enter code %s\n", started.VerificationURI, started.UserCode)
	loginCtx, cancel := context.WithTimeout(ctx, started.ExpiresIn)
	defer cancel()
	wait := started.Interval
	for {
		if err := m.sleep(loginCtx, wait); err != nil {
			return errors.New("login expired or was interrupted")
		}
		result, err := m.client.Poll(loginCtx, started.FlowToken)
		if err != nil {
			return fmt.Errorf("complete login: %w", err)
		}
		if result.Pending {
			if result.RetryAfter > 0 {
				wait = result.RetryAfter
			}
			continue
		}
		if err := m.store.Save(result.Session); err != nil {
			return fmt.Errorf("store device credential: %w", err)
		}
		fmt.Fprintf(output, "Signed in as %s.\n", result.Session.Login)
		return nil
	}
}

// Refresh rotates the stored credential without changing device identity.
func (m *Manager) Refresh(ctx context.Context) error {
	session, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load device credential: %w", err)
	}
	replacement, err := m.client.Refresh(ctx, session.Credential)
	if err != nil {
		return fmt.Errorf("refresh device credential: %w", err)
	}
	if replacement.Login == "" {
		replacement.Login = session.Login
	}
	if err := m.store.Save(replacement); err != nil {
		return fmt.Errorf("store refreshed credential: %w", err)
	}
	return nil
}

// Logout revokes the server device before deleting its local credential.
func (m *Manager) Logout(ctx context.Context) error {
	session, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load device credential: %w", err)
	}
	err = m.client.Logout(ctx, session.Credential)
	if err != nil && !errors.Is(err, ErrUnauthorized) {
		return fmt.Errorf("revoke device: %w", err)
	}
	if err := m.store.Delete(); err != nil {
		return fmt.Errorf("delete device credential: %w", err)
	}
	return nil
}

// DeleteAccount permanently erases hosted account data before removing the
// local device session. A failed or unauthorized request keeps the credential
// so the user can retry or reauthenticate without a false success signal.
func (m *Manager) DeleteAccount(ctx context.Context) error {
	session, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("load device credential: %w", err)
	}
	if err := m.client.DeleteAccount(ctx, session.Credential); err != nil {
		return fmt.Errorf("delete hosted account: %w", err)
	}
	if err := m.store.Delete(); err != nil {
		return fmt.Errorf("delete device credential: %w", err)
	}
	return nil
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
