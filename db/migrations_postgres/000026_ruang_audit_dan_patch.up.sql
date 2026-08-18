-- Isu #23 fase 1: ruang adalah satu-satunya tabel master yang belum punya jejak
-- perubahan sama sekali. Migrasi 000009 memberi created_at/created_by/updated_at/
-- updated_by ke satuan, ekspedisi, supplier, dan pelanggan; ruang terlewat waktu
-- itu. Bentuknya persis sama dengan keempat tabel itu.
ALTER TABLE ruang
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN created_by BIGINT,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

ALTER TABLE ruang
    ADD CONSTRAINT ruang_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT ruang_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

-- updated_at diurus trigger, bukan aplikasi — fungsinya sudah ada dari migrasi
-- 000001 dan dipakai ulang di sini, sama seperti keempat tabel master lainnya.
CREATE TRIGGER ruang_set_updated_at
    BEFORE UPDATE ON ruang
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
