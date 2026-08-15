-- Pemakaian internal: menepati janji kosakata status, dan urutan baca daftar
-- dokumen (isu #9).
--
-- Tabel pemakaian dan pemakaian_detail sudah dibuat migrasi 000007, dan nilai
-- enum 'PEMAKAIAN' serta 'PEMBATALAN_PEMAKAIAN' sudah ada di jenis_transaksi
-- sejak 000002. Jadi **tidak ada ALTER TYPE sama sekali** di sini, sama seperti
-- 000018 untuk mutasi.
--
-- 000018 menuliskannya sebagai utang, bukan usulan: kosakatanya POSTED/BATAL,
-- mengikuti pembelian, bukan DIPOSTING/DIBATALKAN yang dipakai 000007 untuk
-- pemakaian. mutasi belum punya CHECK jadi masih bebas memilih, dan membiarkan
-- dua kosakata hidup berdampingan berarti setiap pembaca harus mengingat mana
-- yang mana. Gilirannya tiba sekarang -- belum ada satu baris data pun di tabel
-- ini, jadi ini perubahan yang paling murah hari ini dan paling mahal setahun
-- lagi.
--
--   DRAFT -> DIAJUKAN -> DISETUJUI -> POSTED
--                    \-> DITOLAK
--                                  \-> BATAL (dari POSTED)
--
-- DITOLAK tetap terminal, tidak kembali ke DRAFT -- berbeda dari pembelian.
-- Penolakan di sini adalah keputusan bisnis (barangnya tidak diberikan), bukan
-- "kertasnya salah ketik". Mengembalikannya ke DRAFT mengaburkan penolakan jadi
-- revisi dan menghapus satu-satunya jejak bahwa permintaan itu pernah ditolak.
ALTER TABLE pemakaian
    DROP CONSTRAINT pemakaian_status_check;

ALTER TABLE pemakaian
    ADD CONSTRAINT pemakaian_status_check CHECK (
        status IN ('DRAFT', 'DIAJUKAN', 'DISETUJUI', 'DITOLAK', 'POSTED', 'BATAL')
    );

-- Mendukung ORDER BY tanggal DESC, id DESC pada endpoint list. pemakaian_tanggal_idx
-- dari 000007 hanya pada (tanggal) dan menaik, jadi ia tidak menopang urutan itu --
-- dan tanpa pemecah seri yang unik, satu baris bisa muncul di dua halaman sementara
-- baris lain tidak pernah keluar. Sama seperti yang dilakukan 000012, 000014, dan
-- 000018 untuk dokumen-dokumen lain.
DROP INDEX pemakaian_tanggal_idx;

CREATE INDEX pemakaian_tanggal_id_idx ON pemakaian (tanggal DESC, id DESC);

-- Sengaja TIDAK ditambahkan: unique index (id_pemakaian, id_product).
-- Ikut mutasi, bukan penerimaan_susulan/retur_pembelian/pembayaran_utang: di
-- sana kuota dipegang baris induk sehingga dua baris untuk sumber yang sama
-- lolos sendiri-sendiri lalu bersama-sama melampauinya. Di sini kuotanya saldo
-- ruang, dijumlahkan per produk sebelum diperiksa dan diperiksa lagi oleh
-- trigger kartu_stok pada setiap insert. Dua baris produk sama dengan satuan
-- berbeda -- 1 DUS untuk bengkel, 3 PCS untuk kantor -- adalah permintaan yang
-- sah, dan keterangan per baris memang untuk itu.
