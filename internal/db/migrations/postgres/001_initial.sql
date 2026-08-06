CREATE TABLE users (
    sub          TEXT PRIMARY KEY,
    issuer       TEXT        NOT NULL,
    email        TEXT        NOT NULL DEFAULT '',
    display_name TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_sub   TEXT        NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_user_sub_idx ON sessions (user_sub);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- A user may register several Spliit instances (spliit.app plus self-hosted).
CREATE TABLE servers (
    id         TEXT PRIMARY KEY,
    user_sub   TEXT        NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    name       TEXT        NOT NULL,
    base_url   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (user_sub, name)
);

CREATE INDEX servers_user_sub_idx ON servers (user_sub);

-- Spliit has no authentication: the group ID is the capability. These rows are
-- the reason this service exists, and why they sit behind OIDC.
CREATE TABLE groups (
    id               TEXT PRIMARY KEY,
    user_sub         TEXT        NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    server_id        TEXT        NOT NULL REFERENCES servers (id) ON DELETE RESTRICT,
    spliit_group_id  TEXT        NOT NULL,
    alias            TEXT        NOT NULL,
    participant_id   TEXT        NOT NULL DEFAULT '',
    participant_name TEXT        NOT NULL DEFAULT '',
    group_name       TEXT        NOT NULL DEFAULT '',
    currency         TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL,
    -- The same group ID on two instances is two different groups.
    UNIQUE (server_id, spliit_group_id),
    -- Aliases are unique per user across servers, so MCP tools take a bare name.
    UNIQUE (user_sub, alias)
);

CREATE INDEX groups_user_sub_idx ON groups (user_sub);
CREATE INDEX groups_server_id_idx ON groups (server_id);
