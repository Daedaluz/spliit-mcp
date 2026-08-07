-- Scope group uniqueness to the user. See the Postgres copy for why.
--
-- SQLite cannot drop a table constraint, so the table is rebuilt.

CREATE TABLE groups_new (
    id               TEXT PRIMARY KEY,
    user_sub         TEXT      NOT NULL REFERENCES users (sub) ON DELETE CASCADE,
    base_url         TEXT      NOT NULL,
    spliit_group_id  TEXT      NOT NULL,
    alias            TEXT      NOT NULL,
    participant_id   TEXT      NOT NULL DEFAULT '',
    participant_name TEXT      NOT NULL DEFAULT '',
    group_name       TEXT      NOT NULL DEFAULT '',
    currency         TEXT      NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL,
    -- Per user: the same group registered by two people is two rows.
    UNIQUE (user_sub, base_url, spliit_group_id),
    UNIQUE (user_sub, alias)
);

INSERT INTO groups_new (id, user_sub, base_url, spliit_group_id, alias,
                        participant_id, participant_name, group_name, currency,
                        created_at, updated_at)
SELECT id, user_sub, base_url, spliit_group_id, alias,
       participant_id, participant_name, group_name, currency,
       created_at, updated_at
FROM groups;

DROP TABLE groups;
ALTER TABLE groups_new RENAME TO groups;

CREATE INDEX groups_user_sub_idx ON groups (user_sub);
CREATE INDEX groups_base_url_idx ON groups (base_url);
