DROP INDEX user_role_grant_global_uidx;
DROP INDEX user_role_grant_uidx;

-- Fails if any user has been granted the same role in two different units since
-- this migration went up — recreating the old (user_id, role_id) uniqueness is
-- only possible once that data no longer exists. That is the down migration
-- correctly refusing to silently collapse two real grants into one.
CREATE UNIQUE INDEX user_role_user_role_uidx ON user_role (user_id, role_id);

ALTER TABLE user_role DROP CONSTRAINT user_role_id_unit_kerja_fkey;
ALTER TABLE user_role DROP COLUMN id_unit_kerja;
