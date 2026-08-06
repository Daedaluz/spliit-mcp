CREATE TABLE users (
    sub          TEXT PRIMARY KEY,
    issuer       TEXT NOT NULL,
    email        TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_sub   TEXT NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX sessions_user_sub_idx ON sessions (user_sub);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- Spliit has no authentication: the group ID is the capability. These rows are
-- the reason this service exists, and why they sit behind OIDC.
--
-- The hosting instance is a column rather than its own table: a Spliit server is
-- never managed independently of the groups on it, and its tRPC base URL can be
-- derived from the group link, so a separate registry would carry no
-- information the join does not already have.
CREATE TABLE groups (
    id               TEXT PRIMARY KEY,
    user_sub         TEXT NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    -- tRPC base URL of the Spliit instance hosting this group.
    base_url         TEXT NOT NULL,
    spliit_group_id  TEXT NOT NULL,
    alias            TEXT NOT NULL,
    participant_id   TEXT NOT NULL DEFAULT '',
    participant_name TEXT NOT NULL DEFAULT '',
    group_name       TEXT NOT NULL DEFAULT '',
    currency         TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL,
    -- The same group ID on two instances is two different groups.
    UNIQUE (base_url, spliit_group_id),
    -- Aliases are unique per user across instances, so MCP tools take a bare name.
    UNIQUE (user_sub, alias)
);

CREATE INDEX groups_user_sub_idx ON groups (user_sub);
CREATE INDEX groups_base_url_idx ON groups (base_url);
