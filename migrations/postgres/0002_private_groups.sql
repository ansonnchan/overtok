-- Private groups are account-scoped. Invite plaintext is returned once and
-- never persisted; only its one-way hash crosses the repository boundary.

CREATE TABLE groups (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    timezone TEXT NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 128),
    owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX groups_owner_user_id ON groups(owner_user_id);

CREATE TABLE group_memberships (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (group_id, user_id),
    CHECK (revoked_at IS NULL OR revoked_at >= joined_at)
);

CREATE UNIQUE INDEX group_memberships_one_owner
    ON group_memberships(group_id)
    WHERE role = 'owner';
CREATE INDEX group_memberships_active_user
    ON group_memberships(user_id, group_id)
    WHERE revoked_at IS NULL;

CREATE TABLE group_invites (
    id UUID PRIMARY KEY,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(code_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    max_uses INTEGER NOT NULL CHECK (max_uses BETWEEN 1 AND 100),
    use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count BETWEEN 0 AND max_uses),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX group_invites_group_id ON group_invites(group_id);

COMMENT ON TABLE groups IS 'Private groups visible only through active membership authorization.';
COMMENT ON TABLE group_invites IS 'Expiring, usage-limited group invitations; plaintext invite codes are never stored.';
