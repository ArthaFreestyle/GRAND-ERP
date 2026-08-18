-- Kebalikan dari 000026: trigger dan constraint dulu, kolom terakhir.
DROP TRIGGER ruang_set_updated_at ON ruang;

ALTER TABLE ruang
    DROP CONSTRAINT ruang_created_by_fkey,
    DROP CONSTRAINT ruang_updated_by_fkey;

ALTER TABLE ruang
    DROP COLUMN created_at,
    DROP COLUMN created_by,
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;
