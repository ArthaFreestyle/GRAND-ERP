-- Isu #21 fase 1: seri document_counter per unit kerja — utang keputusan isu
-- #12 fase 1 yang jatuh tempo sekarang, sebelum outlet kedua benar-benar
-- jalan. Begitu sebuah nomor tercetak di faktur yang dipegang supplier,
-- kuncinya tidak bisa diubah lagi di bawahnya.
--
-- id_unit_kerja NULL berarti seri global — bentuk grant SUPERADMIN dan bentuk
-- setiap baris document_counter yang ada sebelum migrasi ini (di-backfill
-- NULL secara implisit oleh ADD COLUMN tanpa DEFAULT). Nomor yang sudah
-- terbit tidak boleh berubah, dan seri lama harus tetap lanjut tanpa
-- mengulang dari 1 — persis yang didapat NULL secara gratis, tanpa satu
-- statement UPDATE pun.
ALTER TABLE document_counter
    ADD COLUMN id_unit_kerja BIGINT REFERENCES unit_kerja (id);

-- PK lama (prefix, tahun, bulan) tidak lagi cukup: dua outlet menerbitkan BL
-- di bulan yang sama harus punya deret masing-masing. Sama seperti
-- user_role.id_unit_kerja di migrasi 000020 — indeks unik biasa tidak
-- membatasi NULL, jadi ini dua indeks, bukan satu PK yang diperlebar.
ALTER TABLE document_counter DROP CONSTRAINT document_counter_pkey;

CREATE UNIQUE INDEX document_counter_scoped_uidx
    ON document_counter (prefix, id_unit_kerja, tahun, bulan);

-- Indeks kedua yang menutup celah NULL di atas. Wajib: tanpa ini, seri global
-- yang sama (id_unit_kerja IS NULL) bisa disisipkan berkali-kali untuk
-- pasangan (prefix, tahun, bulan) yang sama dan tidak ada yang menolaknya.
CREATE UNIQUE INDEX document_counter_global_uidx
    ON document_counter (prefix, tahun, bulan)
    WHERE id_unit_kerja IS NULL;
