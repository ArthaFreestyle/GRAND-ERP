-- Kebalikan dari 000009. Urutannya terbalik dari up: indeks dan trigger dulu,
-- kolom terakhir.
--
-- Turun selalu aman: UNIQUE (kode) yang dipasang ulang lebih longgar daripada
-- indeks pada lower(kode), jadi data apa pun yang lolos indeks baru pasti lolos
-- constraint lama. Arah naik yang bisa gagal — kalau database sudah menyimpan
-- dua kode yang hanya beda kapital, duplikatnya harus dibereskan lebih dulu.

DROP INDEX satuan_nama_idx;
DROP INDEX ekspedisi_nama_idx;
DROP INDEX pelanggan_nama_idx;
DROP INDEX supplier_nama_idx;

DROP TRIGGER pelanggan_set_updated_at ON pelanggan;
DROP TRIGGER supplier_set_updated_at ON supplier;
DROP TRIGGER ekspedisi_set_updated_at ON ekspedisi;
DROP TRIGGER satuan_set_updated_at ON satuan;

ALTER TABLE pelanggan DROP CONSTRAINT pelanggan_updated_by_fkey;
ALTER TABLE supplier  DROP CONSTRAINT supplier_updated_by_fkey;

ALTER TABLE ekspedisi
    DROP CONSTRAINT ekspedisi_created_by_fkey,
    DROP CONSTRAINT ekspedisi_updated_by_fkey;

ALTER TABLE satuan
    DROP CONSTRAINT satuan_created_by_fkey,
    DROP CONSTRAINT satuan_updated_by_fkey;

ALTER TABLE pelanggan
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;

ALTER TABLE supplier
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;

ALTER TABLE ekspedisi
    DROP COLUMN created_at,
    DROP COLUMN created_by,
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;

ALTER TABLE satuan
    DROP COLUMN created_at,
    DROP COLUMN created_by,
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;

ALTER TABLE satuan DROP COLUMN is_aktif;

DROP INDEX ekspedisi_nama_lower_uidx;

DROP INDEX satuan_nama_lower_uidx;
DROP INDEX ruang_kode_lower_uidx;
DROP INDEX pelanggan_kode_lower_uidx;
DROP INDEX supplier_kode_lower_uidx;

ALTER TABLE satuan    ADD CONSTRAINT satuan_nama_key    UNIQUE (nama);
ALTER TABLE ruang     ADD CONSTRAINT ruang_kode_key     UNIQUE (kode);
ALTER TABLE pelanggan ADD CONSTRAINT pelanggan_kode_key UNIQUE (kode);
ALTER TABLE supplier  ADD CONSTRAINT supplier_kode_key  UNIQUE (kode);
