-- Retur pembelian: barang yang dikembalikan ke supplier (isu #4 fase 5).
--
-- Tabel retur_pembelian dan retur_pembelian_detail sudah dibuat migrasi 000005.
-- Yang ditambahkan di sini adalah tiga hal yang belum ada di sana, dan semuanya
-- baru dibutuhkan sekarang karena dokumen ini akhirnya punya lapisan Go:
--
--   1. alur persetujuan  -- DRAFT -> DIAJUKAN -> POSTED -> BATAL, sama seperti
--                           pembelian dan penerimaan_susulan
--   2. kolom pembatalan  -- dokumen ini menulis kartu_stok, jadi ia harus bisa
--                           dibalik, dan pembalikan tanpa catatan siapa serta
--                           kenapa tidak bisa dibedakan dari kekeliruan
--   3. penjaga keunikan  -- satu baris pembelian hanya sekali per dokumen retur
--
-- Tidak ada nilai enum baru: 'RETUR_PEMBELIAN' sudah ada di jenis_transaksi sejak
-- migrasi 000002. Itu kebetulan yang menyenangkan -- ALTER TYPE ... ADD VALUE
-- tidak bisa dibatalkan, jadi migrasi yang tidak perlu menambahnya juga tidak
-- perlu meninggalkan jejak yang tidak bisa dihapus.

-- ---------------------------------------------------------------------------
-- 1. Alur persetujuan dan pembatalan
-- ---------------------------------------------------------------------------
--
-- Alasannya sama persis seperti pembelian: posting menulis ke kartu_stok yang
-- append-only, jadi posting yang salah hanya bisa dibalik, tidak diperbaiki.
-- INVENTARIS yang mengemas barang dan mengetik dokumennya lalu mengajukan;
-- SUPERADMIN yang memutuskan angkanya boleh keluar dari buku stok.
--
--   DRAFT --ajukan--> DIAJUKAN --posting--> POSTED --batal--> BATAL
--                        `------tolak------> DRAFT
ALTER TABLE retur_pembelian
    ADD COLUMN diajukan_oleh   BIGINT,
    ADD COLUMN diajukan_pada   TIMESTAMPTZ,
    ADD COLUMN disetujui_oleh  BIGINT,
    ADD COLUMN disetujui_pada  TIMESTAMPTZ,
    ADD COLUMN dibatalkan_oleh BIGINT,
    ADD COLUMN alasan_batal    TEXT,
    ADD COLUMN alasan_tolak    TEXT;

ALTER TABLE retur_pembelian
    ADD CONSTRAINT retur_pembelian_status_check
        CHECK (status IN ('DRAFT', 'DIAJUKAN', 'POSTED', 'BATAL')),
    -- total adalah nilai persediaan yang keluar, dijumlahkan ulang dari barisnya.
    -- Negatif berarti ada baris yang harga pokoknya negatif, yang tidak mungkin.
    ADD CONSTRAINT retur_pembelian_total_check CHECK (total >= 0),
    ADD CONSTRAINT retur_pembelian_diajukan_oleh_fkey
        FOREIGN KEY (diajukan_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT retur_pembelian_disetujui_oleh_fkey
        FOREIGN KEY (disetujui_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT retur_pembelian_dibatalkan_oleh_fkey
        FOREIGN KEY (dibatalkan_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

CREATE INDEX retur_pembelian_status_idx ON retur_pembelian (status);

-- ---------------------------------------------------------------------------
-- 2. Urutan baca daftar dokumen
-- ---------------------------------------------------------------------------
--
-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. Indeks yang dibuat
-- 000005 hanya pada (tanggal), dan indeks satu kolom tidak melayani pemecah
-- serinya -- tanpa pemecah seri yang unik, satu baris bisa muncul di dua halaman
-- sementara baris lain tidak pernah keluar. Sama seperti yang dilakukan 000012
-- untuk pembelian.
DROP INDEX retur_pembelian_tanggal_idx;

CREATE INDEX retur_pembelian_tanggal_id_idx ON retur_pembelian (tanggal DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 3. Penjaga baris
-- ---------------------------------------------------------------------------
--
-- Satu baris pembelian hanya boleh muncul sekali per dokumen retur. Tanpa ini,
-- dua baris untuk sumber yang sama akan lolos pengecekan kuota sendiri-sendiri
-- lalu bersama-sama melebihinya -- persis jebakan yang sama seperti
-- penerimaan_susulan_detail_baris_uidx di migrasi 000013.
CREATE UNIQUE INDEX retur_pembelian_detail_baris_uidx
    ON retur_pembelian_detail (id_retur_pembelian, id_pembelian_detail);

ALTER TABLE retur_pembelian_detail
    ADD CONSTRAINT retur_pembelian_detail_nilai_check CHECK (nilai >= 0);
