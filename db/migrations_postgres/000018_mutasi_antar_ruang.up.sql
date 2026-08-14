-- Mutasi antar ruang: satu dokumen, dua baris kartu stok (isu #7).
--
-- Tabel mutasi dan mutasi_detail sudah dibuat migrasi 000007, dan nilai enum
-- 'MUTASI_KELUAR' serta 'MUTASI_MASUK' sudah ada di jenis_transaksi sejak 000002.
-- Jadi **tidak ada ALTER TYPE sama sekali** di sini, sama beruntungnya dengan
-- retur_pembelian di 000014 -- ALTER TYPE ... ADD VALUE tidak bisa dibatalkan,
-- jadi migrasi yang tidak perlu menambah nilai juga tidak meninggalkan jejak
-- yang tidak bisa dihapus.
--
-- Yang ditambahkan di sini persis daftar yang sama seperti 000014:
--
--   1. alur status    -- DRAFT -> POSTED -> BATAL, **tanpa DIAJUKAN**
--   2. kolom pembatalan -- dokumen ini menulis kartu_stok, jadi harus bisa dibalik
--   3. urutan baca daftar dokumen

-- ---------------------------------------------------------------------------
-- 1. Alur status, dan kenapa tanpa DIAJUKAN
-- ---------------------------------------------------------------------------
--
-- status selama ini VARCHAR bebas tanpa CHECK. Isinya sekarang dikunci tiga
-- nilai, dan ketiadaan DIAJUKAN adalah keputusan, bukan kelalaian.
--
-- Aturan yang berlaku untuk tiga penulis kartu_stok sebelumnya berbunyi: setiap
-- modul yang menulis kartu_stok memisah penjaganya per tahap alur, karena tabel
-- itu append-only -- posting yang salah hanya bisa dibalik, dan pembaliknya
-- dinilai dengan rata-rata bergerak yang mungkin sudah bergeser. Tahap
-- persetujuan adalah harga yang dibayar untuk kesalahan yang tidak bisa dihapus.
--
-- Yang membedakan mutasi: kesalahannya jauh lebih murah. Pembelian yang salah
-- membentuk utang ke pihak luar; retur yang salah mengurangi utang dan membekukan
-- kredit. Mutasi yang salah hanya mencatat barang di ruang yang keliru -- total
-- stok dan total nilai persediaan tidak bergerak sedikit pun. Tidak ada pihak
-- luar, tidak ada uang, dan koreksinya adalah mutasi lagi ke arah sebaliknya,
-- dokumen yang orang yang sama sudah berwenang membuatnya.
--
-- Alasannya berbeda dari pembayaran_utang, yang juga tidak punya DIAJUKAN. Di
-- sana pembenarannya adalah alokasi tidak meninggalkan residu. Mutasi tidak
-- seberuntung itu: pembatalannya tetap meninggalkan residu nilai, karena baris
-- keluar dari ruang tujuan dinilai dengan rata-rata bergerak ruang tujuan saat
-- itu. Pembenarannya bukan kerapian pembatalan, melainkan kecilnya taruhan.
--
-- Kendali dua orangnya tetap utuh, tapi pindah seluruhnya ke tabel rute:
-- INVENTARIS hanya sampai DRAFT, SUPERADMIN yang memposting maupun membatalkan.
-- Yang hilang dari membuang DIAJUKAN cuma state-nya, bukan pemisahan wewenangnya.
--
-- Kosakatanya POSTED/BATAL, mengikuti pembelian, bukan DIPOSTING/DIBATALKAN yang
-- dipakai 000007 untuk pemakaian. mutasi belum punya CHECK jadi masih bebas
-- memilih, dan membiarkan dua kosakata hidup berdampingan berarti setiap pembaca
-- harus mengingat mana yang mana. pemakaian menyesuaikan saat gilirannya tiba.
--
--   DRAFT --posting--> POSTED --batal--> BATAL
ALTER TABLE mutasi
    ADD COLUMN dibatalkan_oleh BIGINT,
    ADD COLUMN alasan_batal    TEXT;

ALTER TABLE mutasi
    ADD CONSTRAINT mutasi_status_check
        CHECK (status IN ('DRAFT', 'POSTED', 'BATAL')),
    ADD CONSTRAINT mutasi_dibatalkan_oleh_fkey
        FOREIGN KEY (dibatalkan_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

CREATE INDEX mutasi_status_idx ON mutasi (status);

-- ---------------------------------------------------------------------------
-- 2. Urutan baca daftar dokumen
-- ---------------------------------------------------------------------------
--
-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. Indeks yang dibuat
-- 000007 hanya pada (tanggal) dan menaik, jadi ia tidak menopang urutan itu --
-- dan tanpa pemecah seri yang unik, satu baris bisa muncul di dua halaman
-- sementara baris lain tidak pernah keluar. Sama seperti yang dilakukan 000012
-- untuk pembelian dan 000014 untuk retur_pembelian.
--
-- Endpoint list juga dipakai SUPERADMIN sebagai antrean kerja, difilter
-- status=DRAFT dan diurutkan paling lama dulu. Tanpa DIAJUKAN tidak ada lagi
-- sinyal "draft ini sudah siap", jadi daftar itulah satu-satunya antreannya --
-- dan mutasi_status_idx di atas yang melayaninya.
DROP INDEX mutasi_tanggal_idx;

CREATE INDEX mutasi_tanggal_id_idx ON mutasi (tanggal DESC, id DESC);

-- ---------------------------------------------------------------------------
-- 3. Yang sengaja TIDAK ditambahkan
-- ---------------------------------------------------------------------------
--
-- Tidak ada mutasi_detail_baris_uidx. penerimaan_susulan, retur_pembelian, dan
-- pembayaran_utang masing-masing melarang satu sumber muncul dua kali dalam satu
-- dokumen, tapi di sana alasannya kuota: dua baris masing-masing lolos sendiri
-- lalu bersama-sama melampaui. Di sini kuotanya saldo ruang asal, dan trigger
-- kartu_stok memeriksanya di **setiap** insert -- baris kedua ditolak sendiri
-- kalau saldonya tidak cukup. Jadi produk yang sama boleh muncul dua kali, sama
-- seperti pembelian, dan itu berguna: 2 DUS dan 3 PCS barang yang sama adalah dua
-- baris yang sah.
--
-- Tidak ada updated_at dan tidak ada trigger set_updated_at(). Tidak satu pun
-- dokumen transaksi di proyek ini punya, dan posted_at sudah menjawab pertanyaan
-- yang berguna.
