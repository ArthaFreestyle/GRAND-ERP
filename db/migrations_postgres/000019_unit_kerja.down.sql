DROP INDEX IF EXISTS ruang_id_unit_kerja_idx;
ALTER TABLE ruang DROP CONSTRAINT ruang_id_unit_kerja_fkey;
ALTER TABLE ruang ALTER COLUMN id_unit_kerja DROP NOT NULL;
ALTER TABLE ruang DROP COLUMN id_unit_kerja;

DROP TRIGGER IF EXISTS unit_kerja_set_updated_at ON unit_kerja;
DROP TABLE unit_kerja;
