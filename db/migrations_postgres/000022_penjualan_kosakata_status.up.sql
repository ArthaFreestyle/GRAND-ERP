-- Modul penjualan: nota keluar dan awal sisi piutang (isu #10).
--
-- Tabel penjualan dan penjualan_detail sudah dibuat migrasi 000006, dan nilai enum
-- 'PENJUALAN' sudah ada di jenis_transaksi sejak 000002. Jadi **tidak ada
-- ALTER TYPE sama sekali** di sini, sama beruntungnya dengan mutasi di 000018 dan
-- pemakaian di 000021.
--
-- Yang ditambahkan persis pola yang sama dengan 000018/000021: kosakata status
-- dikunci CHECK, satu CHECK baru yang belum pernah dijaga sama sekali
-- (penjualan_kredit_pelanggan_check), dan urutan baca daftar dokumen.

-- ---------------------------------------------------------------------------
-- 1. Kosakata status, jenis pembayaran, dan status pembayaran
-- ---------------------------------------------------------------------------
--
-- status dan status_pembayaran selama ini VARCHAR bebas tanpa CHECK, sama seperti
-- mutasi sebelum 000018. jenis_pembayaran juga belum pernah dijaga.
ALTER TABLE penjualan
    ADD CONSTRAINT penjualan_status_check
        CHECK (status IN ('DRAFT', 'POSTED', 'BATAL')),
    ADD CONSTRAINT penjualan_jenis_pembayaran_check
        CHECK (jenis_pembayaran IN ('TUNAI', 'KREDIT')),
    ADD CONSTRAINT penjualan_status_pembayaran_check
        CHECK (status_pembayaran IN ('BELUM', 'SEBAGIAN', 'LUNAS'));

-- ---------------------------------------------------------------------------
-- 2. id_pelanggan wajib pada nota KREDIT
-- ---------------------------------------------------------------------------
--
-- id_pelanggan tetap nullable -- pembeli yang bayar tunai di depan meja tidak perlu
-- didaftarkan, dan memaksanya berarti master pelanggan penuh baris "umum" yang tidak
-- berarti apa-apa. Tapi piutang tanpa pelanggan tidak bisa ditagih siapa-siapa: tidak
-- ada yang bisa dibaca laporan umur piutang, tidak ada plafon_kredit yang bisa
-- diperiksa, dan alokasi pembayaran nanti tidak punya pemilik. Sebelum baris ini,
-- nota KREDIT tanpa pelanggan lolos ke database dan baru ketahuan saat penagihan.
--
-- Ditangkap lebih dulu di Go supaya pesannya menyebut fieldnya -- sama seperti
-- keterangan_selisih dan alasan retur -- tapi CHECK ini tetap yang menjaga
-- sebenarnya.
ALTER TABLE penjualan
    ADD CONSTRAINT penjualan_kredit_pelanggan_check
        CHECK (jenis_pembayaran <> 'KREDIT' OR id_pelanggan IS NOT NULL);

-- ---------------------------------------------------------------------------
-- 3. Urutan baca daftar dokumen
-- ---------------------------------------------------------------------------
--
-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. penjualan_tanggal_idx
-- dari 000006 hanya pada (tanggal) dan menaik, jadi ia tidak menopang urutan itu --
-- dan tanpa pemecah seri yang unik, satu baris bisa muncul di dua halaman sementara
-- baris lain tidak pernah keluar. Sama seperti yang dilakukan 000012, 000014, 000018,
-- dan 000021 untuk dokumen-dokumen lain.
DROP INDEX penjualan_tanggal_idx;

CREATE INDEX penjualan_tanggal_id_idx ON penjualan (tanggal DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 4. Yang sengaja TIDAK ditambahkan
-- ---------------------------------------------------------------------------
--
-- Tidak ada unique index (id_penjualan, id_product). Ikut mutasi dan pemakaian,
-- bukan penerimaan_susulan/retur_pembelian/pembayaran_utang: di sana kuota dipegang
-- baris induk sehingga dua baris untuk sumber yang sama lolos sendiri-sendiri lalu
-- bersama-sama melampauinya. Di sini kuotanya saldo ruang, dijumlahkan per produk
-- sebelum diperiksa dan diperiksa lagi oleh trigger kartu_stok pada setiap insert.
-- Dua baris produk sama dengan satuan berbeda -- 1 DUS dan 3 PCS -- adalah nota yang
-- sah.
