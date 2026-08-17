-- Sisi piutang pelanggan: penerimaan pembayaran dan alokasinya (isu #20).
--
-- Cermin migrasi 000015 (pembayaran_utang), uang mengalir ke arah sebaliknya. Tabel
-- penerimaan_pembayaran dan pembayaran_alokasi sudah dibuat migrasi 000006. Yang
-- ditambahkan di sini adalah penjaga nilai yang belum ada di sana — tidak ada
-- ALTER TYPE sama sekali, dan tidak ada kolom baru di penjualan: retur_penjualan
-- tetap di luar cakupan isu ini, dan tempat untuk kredit returnya disiapkan sebagai
-- fragmen SQL bernilai nol, bukan sebagai kolom baru — lihat penjualanKreditRetur di
-- penjualan_repository.go.

-- ---------------------------------------------------------------------------
-- Penjaga nilai penerimaan_pembayaran
-- ---------------------------------------------------------------------------
--
-- DRAFT -> POSTED -> BATAL, **tanpa DIAJUKAN**, alasannya persis sama seperti di
-- sisi utang: alokasi tidak meninggalkan residu penilaian apa pun, ia bisa
-- dibatalkan dan seluruh cache dihitung ulang persis. Kontrol dua orangnya tetap
-- ada lewat tabel rute, satu state lebih sedikit: yang menyiapkan dokumen bukan
-- yang melepas piutangnya.
--
-- Skema 000006 memang sudah mencerminkan itu: ia memberi tabel ini posted_at,
-- dibatalkan_oleh, dan alasan_batal, tetapi tidak diajukan_oleh maupun
-- disetujui_oleh.
ALTER TABLE penerimaan_pembayaran
    ADD CONSTRAINT penerimaan_pembayaran_status_check
        CHECK (status IN ('DRAFT', 'POSTED', 'BATAL')),
    ADD CONSTRAINT penerimaan_pembayaran_metode_check
        CHECK (metode IN ('TUNAI', 'TRANSFER', 'GIRO')),
    ADD CONSTRAINT penerimaan_pembayaran_status_giro_nilai_check
        CHECK (status_giro IS NULL OR status_giro IN ('BELUM_CAIR', 'CAIR', 'TOLAK')),
    -- status_giro hanya ada artinya untuk giro, dan wajib ada di situ. Giro tanpa
    -- status tidak bisa dibedakan dari giro yang sudah cair, dan itu perbedaan
    -- antara piutang yang berkurang dan yang tidak.
    ADD CONSTRAINT penerimaan_pembayaran_giro_check
        CHECK (
            (metode = 'GIRO' AND status_giro IS NOT NULL)
            OR (metode <> 'GIRO' AND status_giro IS NULL
                AND tanggal_jatuh_tempo_giro IS NULL AND tanggal_cair IS NULL)
        ),
    -- tanggal_cair terisi tepat ketika giro sudah cair. Giro yang ditolak tidak
    -- punya tanggal cair, dan giro yang cair tanpa tanggalnya tidak bisa
    -- direkonsiliasi ke rekening bank.
    --
    -- Ketika status_giro NULL, operand pertama NULL sehingga seluruh perbandingan
    -- NULL, dan `NULL OR TRUE` bernilai TRUE — baris non-giro lolos lewat cabang
    -- kedua.
    ADD CONSTRAINT penerimaan_pembayaran_tanggal_cair_check
        CHECK ((status_giro = 'CAIR') = (tanggal_cair IS NOT NULL) OR status_giro IS NULL);

-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. Indeks satu kolom
-- tidak melayani pemecah serinya, dan tanpa pemecah seri yang unik satu baris bisa
-- muncul di dua halaman sementara baris lain tidak pernah keluar.
DROP INDEX penerimaan_pembayaran_tanggal_idx;

CREATE INDEX penerimaan_pembayaran_tanggal_id_idx ON penerimaan_pembayaran (tanggal DESC, id DESC);

-- Daftar giro pelanggan yang belum cair adalah pekerjaan harian bagian keuangan,
-- dan itu satu filter atas kolom ini.
CREATE INDEX penerimaan_pembayaran_status_giro_idx
    ON penerimaan_pembayaran (status_giro)
    WHERE status_giro IS NOT NULL;

-- Satu nota hanya boleh muncul sekali per pembayaran. Tanpa ini dua baris untuk
-- nota yang sama akan lolos pengecekan sisa sendiri-sendiri lalu bersama-sama
-- melebihinya — jebakan yang sama seperti pembayaran_utang_alokasi_baris_uidx,
-- penerimaan_susulan_detail_baris_uidx, dan retur_pembelian_detail_baris_uidx.
CREATE UNIQUE INDEX pembayaran_alokasi_baris_uidx
    ON pembayaran_alokasi (id_penerimaan_pembayaran, id_penjualan);
