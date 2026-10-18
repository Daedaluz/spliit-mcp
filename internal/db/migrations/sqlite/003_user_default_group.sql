-- The group MCP tools fall back to when a call names none. See the Postgres
-- copy for why leaving the group clears it.
ALTER TABLE users
    ADD COLUMN default_group_id TEXT REFERENCES groups (id) ON DELETE SET NULL;
