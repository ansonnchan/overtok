package group

import (
	"context"
	"errors"
	"testing"
	"time"

	serverauth "github.com/ansonnchan/overtok/server/auth"
)

func TestServiceCreatesValidatedGroup(t *testing.T) {
	repository := &fakeRepository{}
	service, err := NewService(fakeAuthenticator{}, repository)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(t.Context(), "credential", "  Quiet Team  ", "America/Vancouver")
	if err != nil {
		t.Fatal(err)
	}
	if repository.created.Name != "Quiet Team" || repository.created.Timezone != "America/Vancouver" || repository.created.ID == "" || created.Role != RoleOwner {
		t.Fatalf("created group = %#v, input = %#v", created, repository.created)
	}
	for _, test := range []struct {
		name     string
		group    string
		timezone string
	}{
		{name: "blank name", group: " ", timezone: "UTC"},
		{name: "multiline name", group: "Team\nHidden", timezone: "UTC"},
		{name: "unknown timezone", group: "Team", timezone: "Mars/Olympus"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(t.Context(), "credential", test.group, test.timezone); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v", err)
			}
		})
	}
}

func TestServiceCreatesHashedLimitedInvite(t *testing.T) {
	repository := &fakeRepository{}
	service, err := NewService(fakeAuthenticator{}, repository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	invite, err := service.Invite(t.Context(), "credential", testGroupID, time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(invite.Code) != 36 || repository.invite.CodeHash == [32]byte{} || invite.ExpiresAt != now.Add(time.Hour) || repository.invite.MaxUses != 2 {
		t.Fatalf("invite = %#v, stored = %#v", invite, repository.invite)
	}
	if _, err := service.Invite(t.Context(), "credential", testGroupID, time.Minute, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short invite error = %v", err)
	}
	if _, err := service.Invite(t.Context(), "credential", testGroupID, time.Hour, 101); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("large invite error = %v", err)
	}
}

func TestServiceAuthenticatesEveryOperation(t *testing.T) {
	authenticator := fakeAuthenticator{err: serverauth.ErrUnauthorized}
	service, err := NewService(authenticator, &fakeRepository{})
	if err != nil {
		t.Fatal(err)
	}
	operations := []func() error{
		func() error { _, err := service.Create(t.Context(), "bad", "Team", "UTC"); return err },
		func() error { _, err := service.List(t.Context(), "bad"); return err },
		func() error { _, err := service.Get(t.Context(), "bad", testGroupID); return err },
		func() error { _, err := service.Invite(t.Context(), "bad", testGroupID, time.Hour, 1); return err },
		func() error {
			_, err := service.Join(t.Context(), "bad", "ovt_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			return err
		},
		func() error { return service.Leave(t.Context(), "bad", testGroupID) },
		func() error { return service.Revoke(t.Context(), "bad", testGroupID, testUserID) },
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, serverauth.ErrUnauthorized) {
			t.Fatalf("operation %d error = %v", index, err)
		}
	}
}

const (
	testGroupID = "10000000-0000-4000-8000-000000000001"
	testUserID  = "10000000-0000-4000-8000-000000000002"
)

type fakeAuthenticator struct {
	err error
}

func (f fakeAuthenticator) Authenticate(context.Context, string) (serverauth.Principal, error) {
	return serverauth.Principal{UserID: testUserID, DeviceID: "10000000-0000-4000-8000-000000000003"}, f.err
}

type fakeRepository struct {
	created NewGroup
	invite  NewInvite
}

func (f *fakeRepository) CreateGroup(_ context.Context, _ serverauth.Principal, input NewGroup) (Group, error) {
	f.created = input
	return Group{ID: input.ID, Name: input.Name, Timezone: input.Timezone, Role: RoleOwner}, nil
}
func (f *fakeRepository) ListGroups(context.Context, serverauth.Principal) ([]Group, error) {
	return []Group{}, nil
}
func (f *fakeRepository) Group(context.Context, serverauth.Principal, string) (Detail, error) {
	return Detail{}, nil
}
func (f *fakeRepository) CreateInvite(_ context.Context, _ serverauth.Principal, input NewInvite) error {
	f.invite = input
	return nil
}
func (f *fakeRepository) Join(context.Context, serverauth.Principal, [32]byte, time.Time) (Group, error) {
	return Group{}, nil
}
func (f *fakeRepository) Leave(context.Context, serverauth.Principal, string) error { return nil }
func (f *fakeRepository) RevokeMember(context.Context, serverauth.Principal, string, string) error {
	return nil
}
