-- Sisi utang supplier: pembayaran dan alokasinya (isu #4 fase 6, fase terakhir).
--
-- Tabel pembayaran_utang dan pembayaran_utang_alokasi sudah dibuat migrasi 000008.
-- Yang ditambahkan di sini adalah penjaga nilai yang belum ada di sana, plus satu
-- kolom pada retur_pembelian yang menutup keputusan yang sengaja ditunda di fase 5.

-- ---------------------------------------------------------------------------
-- 1. Berapa yang dikreditkan supplier atas barang yang diretur
-- ---------------------------------------------------------------------------
--
-- Fase 5 menulis retur_pembelian.total dari harga pokok, dan itu benar untuk
-- persediaan: harga pokoknya disalin dari baris pembelian supaya pembelian dan
-- returnya saling menghapus di kartu stok. Tapi harga pokok **sudah termasuk porsi
-- ongkir** yang dibayar ke ekspedisi, sementara pembelian.total tidak memasukkan
-- ongkir sama sekali. Mengurangkan yang satu dari yang lain akan melebihkan kredit
-- sebesar uang yang tidak pernah diterima supplier.
--
-- Jadi utang butuh angkanya sendiri:
--
--   nilai_faktur_retur = Σ (pembelian_detail.subtotal / qty_dasar) x qty_retur_dasar
--   nilai_kredit_utang = pembelian.total x nilai_faktur_retur / pembelian.subtotal
--
-- Diskalakan terhadap total, bukan diambil mentah dari nilai baris faktur, karena
-- total sudah memuat diskon nota, PPN, dan pembulatan. Mengembalikan seluruh barang
-- lalu mengkredit seluruh nilai baris akan melebihkan kredit sebesar diskon nota yang
-- justru mengurangi tagihannya. Penskalaan proporsional membuat invariannya bisa
-- diperiksa: **kredit seluruh retur sebuah pembelian tidak pernah melebihi
-- total-nya**, dan pas sebesar total ketika semua barang kembali.
--
-- Dibekukan saat posting, bukan dihitung saat dibaca. Ia turunan dari baris dokumen
-- yang keduanya sudah POSTED dan tidak bisa berubah, jadi menghitungnya ulang akan
-- selalu memberi jawaban sama -- sampai suatu hari tidak, dan saat itu utang lama
-- diam-diam berganti nilai. Setiap angka uang di proyek ini adalah snapshot.
ALTER TABLE retur_pembelian
    ADD COLUMN nilai_kredit_utang NUMERIC(20, 2) NOT NULL DEFAULT 0;

ALTER TABLE retur_pembelian
    ADD CONSTRAINT retur_pembelian_nilai_kredit_utang_check
        CHECK (nilai_kredit_utang >= 0);

-- ---------------------------------------------------------------------------
-- 2. Penjaga nilai pembayaran_utang
-- ---------------------------------------------------------------------------
--
-- DRAFT -> POSTED -> BATAL, **tanpa DIAJUKAN**, dan itu bukan kelalaian meniru
-- pembelian. Tahap persetujuan di pembelian ada karena kartu_stok append-only:
-- posting yang salah tidak bisa diperbaiki, hanya dibalik, dan pembalikannya dinilai
-- pada rata-rata yang sudah bergeser. Alokasi pembayaran tidak begitu -- ia bisa
-- dibatalkan dan seluruh cache dihitung ulang persis, tanpa residu penilaian apa pun.
-- Menambahkan DIAJUKAN di sini berarti meniru mekanisme tanpa alasannya. Kontrol dua
-- orangnya tetap ada, hanya satu state lebih sedikit: yang menyiapkan dokumen bukan
-- yang melepas uangnya.
--
-- Skema 000008 memang sudah mencerminkan itu: ia memberi tabel ini posted_at,
-- dibatalkan_oleh, dan alasan_batal, tetapi tidak diajukan_oleh maupun
-- disetujui_oleh.
ALTER TABLE pembayaran_utang
    ADD CONSTRAINT pembayaran_utang_status_check
        CHECK (status IN ('DRAFT', 'POSTED', 'BATAL')),
    ADD CONSTRAINT pembayaran_utang_metode_check
        CHECK (metode IN ('TUNAI', 'TRANSFER', 'GIRO')),
    ADD CONSTRAINT pembayaran_utang_status_giro_nilai_check
        CHECK (status_giro IS NULL OR status_giro IN ('BELUM_CAIR', 'CAIR', 'TOLAK')),
    -- status_giro hanya ada artinya untuk giro, dan wajib ada di situ. Giro tanpa
    -- status tidak bisa dibedakan dari giro yang sudah cair, dan itu perbedaan antara
    -- utang yang berkurang dan yang tidak.
    ADD CONSTRAINT pembayaran_utang_giro_check
        CHECK (
            (metode = 'GIRO' AND status_giro IS NOT NULL)
            OR (metode <> 'GIRO' AND status_giro IS NULL
                AND tanggal_jatuh_tempo_giro IS NULL AND tanggal_cair IS NULL)
        ),
    -- tanggal_cair terisi tepat ketika giro sudah cair. Giro yang ditolak tidak punya
    -- tanggal cair, dan giro yang cair tanpa tanggalnya tidak bisa direkonsiliasi ke
    -- rekening bank.
    --
    -- Ketika status_giro NULL, operand pertama NULL sehingga seluruh perbandingan
    -- NULL, dan `NULL OR TRUE` bernilai TRUE -- baris non-giro lolos lewat cabang
    -- kedua.
    ADD CONSTRAINT pembayaran_utang_tanggal_cair_check
        CHECK ((status_giro = 'CAIR') = (tanggal_cair IS NOT NULL) OR status_giro IS NULL);

-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. Indeks satu kolom
-- tidak melayani pemecah serinya, dan tanpa pemecah seri yang unik satu baris bisa
-- muncul di dua halaman sementara baris lain tidak pernah keluar.
DROP INDEX pembayaran_utang_tanggal_idx;

CREATE INDEX pembayaran_utang_tanggal_id_idx ON pembayaran_utang (tanggal DESC, id DESC);

-- Daftar giro yang belum cair adalah pekerjaan harian bagian keuangan, dan itu satu
-- filter atas kolom ini.
CREATE INDEX pembayaran_utang_status_giro_idx
    ON pembayaran_utang (status_giro)
    WHERE status_giro IS NOT NULL;

-- Satu pembelian hanya boleh muncul sekali per pembayaran. Tanpa ini dua baris untuk
-- faktur yang sama akan lolos pengecekan sisa sendiri-sendiri lalu bersama-sama
-- melebihinya -- jebakan yang sama seperti penerimaan_susulan_detail_baris_uidx dan
-- retur_pembelian_detail_baris_uidx.
CREATE UNIQUE INDEX pembayaran_utang_alokasi_baris_uidx
    ON pembayaran_utang_alokasi (id_pembayaran_utang, id_pembelian);
