package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	serverauth "github.com/ansonnchan/tokenholic/server/auth"
	servergroup "github.com/ansonnchan/tokenholic/server/group"
)

func TestPostgresPrivateGroupAuthorizationAndInviteLifecycle(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner := serverauth.Principal{UserID: "40000000-0000-4000-8000-000000000001"}
	member := serverauth.Principal{UserID: "40000000-0000-4000-8000-000000000002"}
	outsider := serverauth.Principal{UserID: "40000000-0000-4000-8000-000000000003"}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users(id, display_name) VALUES
		($1::uuid, 'group-owner'), ($2::uuid, 'group-member'), ($3::uuid, 'group-outsider')
	`, owner.UserID, member.UserID, outsider.UserID); err != nil {
		t.Fatal(err)
	}
	repository, err := NewGroupRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	groupID := "40000000-0000-4000-8000-000000000010"
	created, err := repository.CreateGroup(ctx, owner, servergroup.NewGroup{
		ID: groupID, Name: "Synthetic private group", Timezone: "America/Vancouver",
	})
	if err != nil || created.Role != servergroup.RoleOwner {
		t.Fatalf("CreateGroup() = %#v, %v", created, err)
	}
	if _, err := repository.Group(ctx, outsider, groupID); !errors.Is(err, servergroup.ErrNotFound) {
		t.Fatalf("outsider read error = %v", err)
	}
	if groups, err := repository.ListGroups(ctx, outsider); err != nil || len(groups) != 0 {
		t.Fatalf("outsider group list = %#v, %v", groups, err)
	}
	inviteHash := sha256.Sum256([]byte("synthetic-invite-one"))
	invite := servergroup.NewInvite{
		ID: "40000000-0000-4000-8000-000000000020", GroupID: groupID,
		CodeHash: inviteHash, ExpiresAt: time.Now().UTC().Add(time.Hour), MaxUses: 1,
	}
	if err := repository.CreateInvite(ctx, outsider, invite); !errors.Is(err, servergroup.ErrNotFound) {
		t.Fatalf("outsider invite error = %v", err)
	}
	if err := repository.CreateInvite(ctx, owner, invite); err != nil {
		t.Fatal(err)
	}
	joined, err := repository.Join(ctx, member, inviteHash, time.Now().UTC())
	if err != nil || joined.Role != servergroup.RoleMember {
		t.Fatalf("Join() = %#v, %v", joined, err)
	}
	detail, err := repository.Group(ctx, member, groupID)
	if err != nil || len(detail.Members) != 2 || detail.Group.Role != servergroup.RoleMember {
		t.Fatalf("member Group() = %#v, %v", detail, err)
	}
	if err := repository.CreateInvite(ctx, member, servergroup.NewInvite{
		ID: "40000000-0000-4000-8000-000000000021", GroupID: groupID,
		CodeHash: sha256.Sum256([]byte("member-invite")), ExpiresAt: time.Now().UTC().Add(time.Hour), MaxUses: 1,
	}); !errors.Is(err, servergroup.ErrForbidden) {
		t.Fatalf("member invite error = %v", err)
	}
	if _, err := repository.Join(ctx, outsider, inviteHash, time.Now().UTC()); !errors.Is(err, servergroup.ErrInvalidInvite) {
		t.Fatalf("exhausted invite error = %v", err)
	}
	if err := repository.RevokeMember(ctx, member, groupID, outsider.UserID); !errors.Is(err, servergroup.ErrForbidden) {
		t.Fatalf("member revoke error = %v", err)
	}
	if err := repository.RevokeMember(ctx, owner, groupID, member.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Group(ctx, member, groupID); !errors.Is(err, servergroup.ErrNotFound) {
		t.Fatalf("revoked member read error = %v", err)
	}
	if err := repository.Leave(ctx, owner, groupID); !errors.Is(err, servergroup.ErrOwnerMustRemain) {
		t.Fatalf("owner leave error = %v", err)
	}
	rejoinHash := sha256.Sum256([]byte("synthetic-invite-two"))
	if err := repository.CreateInvite(ctx, owner, servergroup.NewInvite{
		ID: "40000000-0000-4000-8000-000000000022", GroupID: groupID,
		CodeHash: rejoinHash, ExpiresAt: time.Now().UTC().Add(time.Hour), MaxUses: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Join(ctx, member, rejoinHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repository.Leave(ctx, member, groupID); err != nil {
		t.Fatal(err)
	}
	listed, err := repository.ListGroups(ctx, member)
	if err != nil || len(listed) != 0 {
		t.Fatalf("groups after leave = %#v, %v", listed, err)
	}
}
