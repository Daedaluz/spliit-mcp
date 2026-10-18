-- The group MCP tools fall back to when a call names none.
--
-- Leaving the group clears it rather than blocking the delete: a dangling
-- default would only surface later as a confusing "group not found".
ALTER TABLE users
    ADD COLUMN default_group_id TEXT REFERENCES groups (id) ON DELETE SET NULL;
