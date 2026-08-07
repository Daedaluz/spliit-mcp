-- Scope group uniqueness to the user.
--
-- Registering a group is per user: two people may each make the same Spliit
-- group available to themselves. The previous constraint omitted user_sub, so
-- the first person to join a group locked everyone else out of it — the insert
-- failed on a key that said nothing about who was inserting.
--
-- This was lost when the servers table was folded into groups: the old
-- (server_id, spliit_group_id) was implicitly per-user, because servers rows
-- were.

ALTER TABLE groups
    DROP CONSTRAINT IF EXISTS groups_base_url_spliit_group_id_key;

ALTER TABLE groups
    ADD CONSTRAINT groups_user_base_url_group_key
    UNIQUE (user_sub, base_url, spliit_group_id);
