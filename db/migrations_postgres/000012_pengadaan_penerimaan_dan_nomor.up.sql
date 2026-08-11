-- Fondasi modul pengadaan: isu #4 fase 1 (penomoran dokumen) dan fase 2
-- (pembelian dengan dua kolom kuantitas serta alur persetujuan).
--
-- Migrasi 000005 dan 000008 sudah membuat pembelian/pembelian_detail. Yang
-- ditambahkan di sini adalah tiga hal yang tidak ada di sana:
--
--   1. document_counter -- generator nomor dokumen, dipakai ulang lintas modul
--   2. kuantitas ganda   -- faktur vs yang benar-benar datang
--   3. alur persetujuan  -- DRAFT -> DIAJUKAN -> POSTED, dan penjaga statusnya

-- ---------------------------------------------------------------------------
-- 1. Penomoran dokumen
-- ---------------------------------------------------------------------------
--
-- Setiap tabel transaksi punya `nomor VARCHAR NOT NULL UNIQUE` dan sebelum ini
-- tidak ada satu pun generatornya. Counter disimpan per (prefix, tahun, bulan)
-- supaya nomor mereset tiap bulan tanpa DDL -- yang tidak bisa dilakukan
-- sequence PostgreSQL.
--
-- Pengambilan nomor adalah satu INSERT ... ON CONFLICT DO UPDATE RETURNING.
-- Itu atomik dan mengunci barisnya sampai transaksi selesai, jadi dua request
-- bersamaan untuk prefix dan bulan yang sama antre, bukan menghasilkan nomor
-- kembar. Konsekuensinya nomor bisa berlubang kalau transaksi dokumennya
-- rollback -- itu harga yang dibayar untuk tidak pernah kembar, dan lubang
-- nomor jauh lebih murah daripada dua dokumen bernomor sama.
CREATE TABLE document_counter (
    prefix      VARCHAR     NOT NULL,
    tahun       INT         NOT NULL,
    bulan       INT         NOT NULL,
    last_number BIGINT      NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT document_counter_pkey             PRIMARY KEY (prefix, tahun, bulan),
    CONSTRAINT document_counter_bulan_check      CHECK (bulan BETWEEN 1 AND 12),
    CONSTRAINT document_counter_last_number_check CHECK (last_number >= 0)
);

-- ---------------------------------------------------------------------------
-- 2. pembelian_detail: faktur vs yang datang
-- ---------------------------------------------------------------------------
--
-- Aturannya: stok ikut barang, utang ikut faktur.
--
--   qty_faktur / qty_dasar                 -> yang tertulis di kertas
--   qty_diterima / qty_diterima_dasar      -> hasil hitung fisik dari box
--
-- qty_input diganti nama menjadi qty_faktur karena di tabel ini kolom itu
-- memang selalu berarti "yang tertulis di faktur"; nama lamanya cuma menyisakan
-- pertanyaan qty yang mana. Tabel ini belum pernah punya lapisan Go, jadi belum
-- ada data yang ikut berganti arti.
ALTER TABLE pembelian_detail
    RENAME COLUMN qty_input TO qty_faktur;

ALTER TABLE pembelian_detail
    -- Dalam satuan input, seperti qty_faktur. Nol berarti barisnya belum datang
    -- sama sekali -- sah, dan bukan penghalang posting.
    ADD COLUMN qty_diterima       NUMERIC(18, 4) NOT NULL DEFAULT 0,
    -- Dalam satuan dasar. Angka inilah yang masuk kartu_stok.
    ADD COLUMN qty_diterima_dasar BIGINT         NOT NULL DEFAULT 0,
    -- Wajib diisi aplikasi saat ada selisih; itu yang jadi catatan waktu
    -- menghubungi supplier lewat WhatsApp.
    ADD COLUMN keterangan_selisih TEXT,

    ADD CONSTRAINT pembelian_detail_qty_faktur_check
        CHECK (qty_faktur > 0),
    -- Kirim lebih ditolak: barang yang tidak difakturkan tidak punya nilai, dan
    -- nilai_masuk proporsionalnya akan melebihi 100% nilai faktur -- harga pokok
    -- rata-rata bergerak jadi salah secara permanen karena kartu_stok
    -- append-only.
    ADD CONSTRAINT pembelian_detail_qty_diterima_check
        CHECK (qty_diterima >= 0 AND qty_diterima <= qty_faktur),
    ADD CONSTRAINT pembelian_detail_qty_diterima_dasar_check
        CHECK (qty_diterima_dasar >= 0 AND qty_diterima_dasar <= qty_dasar);

-- ---------------------------------------------------------------------------
-- 3. pembelian: persetujuan, cache penerimaan, dan penjaga enum
-- ---------------------------------------------------------------------------
--
-- Posting pembelian menulis ke kartu_stok, dan kartu_stok append-only: salah
-- posting hanya bisa dibalik, tidak bisa diperbaiki. Karena itu penulisannya
-- lewat persetujuan. INVENTARIS mengetik faktur dan menghitung barang lalu
-- mengajukan; SUPERADMIN yang memutuskan angkanya boleh masuk kartu stok.
--
--   DRAFT --ajukan--> DIAJUKAN --posting--> POSTED --batal--> BATAL
--                        `------tolak------> DRAFT
ALTER TABLE pembelian
    ADD COLUMN diajukan_oleh  BIGINT,
    ADD COLUMN diajukan_pada  TIMESTAMPTZ,
    ADD COLUMN disetujui_oleh BIGINT,
    ADD COLUMN disetujui_pada TIMESTAMPTZ,
    ADD COLUMN alasan_tolak   TEXT,
    -- Cache, mengikuti pola status_pembayaran: selalu dihitung ulang dari baris
    -- detail saat posting, tidak pernah di-set dari form. LENGKAP untuk dokumen
    -- yang belum diposting berarti "belum ada selisih yang tercatat", bukan
    -- klaim bahwa barangnya sudah datang.
    ADD COLUMN status_penerimaan VARCHAR NOT NULL DEFAULT 'LENGKAP';

ALTER TABLE pembelian
    ADD CONSTRAINT pembelian_status_check
        CHECK (status IN ('DRAFT', 'DIAJUKAN', 'POSTED', 'BATAL')),
    ADD CONSTRAINT pembelian_status_penerimaan_check
        CHECK (status_penerimaan IN ('LENGKAP', 'KURANG')),
    ADD CONSTRAINT pembelian_status_pembayaran_check
        CHECK (status_pembayaran IN ('BELUM', 'SEBAGIAN', 'LUNAS')),
    ADD CONSTRAINT pembelian_jenis_pembayaran_check
        CHECK (jenis_pembayaran IN ('TUNAI', 'KREDIT')),
    ADD CONSTRAINT pembelian_metode_alokasi_angkut_check
        CHECK (metode_alokasi_angkut IN ('KOLI', 'QTY')),
    ADD CONSTRAINT pembelian_diajukan_oleh_fkey
        FOREIGN KEY (diajukan_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT pembelian_disetujui_oleh_fkey
        FOREIGN KEY (disetujui_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

CREATE INDEX pembelian_status_penerimaan_idx ON pembelian (status_penerimaan);

-- Tanpa purchase order, dokumen ini satu-satunya jejak faktur supplier. Kalau
-- nota yang sama diinput dua kali tidak ada yang menahannya dan stok naik dua
-- kali -- kesalahan yang tidak bisa diperbaiki, hanya dibalik.
--
-- Case-insensitive lewat lower(), mengikuti pola keunikan master di migrasi
-- 000009. Dokumen BATAL dikecualikan supaya nota yang salah input bisa
-- dibatalkan lalu diinput ulang dengan nomor faktur yang sama.
CREATE UNIQUE INDEX pembelian_supplier_faktur_uidx
    ON pembelian (id_supplier, lower(no_faktur_supplier))
    WHERE no_faktur_supplier IS NOT NULL AND status <> 'BATAL';

-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. Index yang sudah
-- ada hanya pada (tanggal), dan indeks satu kolom tidak melayani pemecah
-- serinya -- tanpa pemecah seri yang terindeks, satu baris bisa muncul di dua
-- halaman sementara baris lain tidak pernah keluar.
DROP INDEX pembelian_tanggal_idx;

CREATE INDEX pembelian_tanggal_id_idx ON pembelian (tanggal DESC, id DESC);
