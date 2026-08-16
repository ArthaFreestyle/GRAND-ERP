-- Kebalikan dari 000022.
--
-- Tabelnya tidak ikut dibuang: penjualan dan penjualan_detail dibuat migrasi 000006,
-- jadi yang ini hanya melepas apa yang ia tambahkan.
--
-- Setelah ini status, jenis_pembayaran, dan status_pembayaran kembali VARCHAR bebas,
-- dan nota KREDIT tanpa pelanggan tidak lagi ditolak database. Baris yang sudah ada
-- tetap apa adanya. Jangan jalankan di database yang sudah punya nota POSTED atau
-- BATAL dan mengandalkan CHECK-nya.

DROP INDEX penjualan_tanggal_id_idx;

CREATE INDEX penjualan_tanggal_idx ON penjualan (tanggal);

ALTER TABLE penjualan
    DROP CONSTRAINT penjualan_kredit_pelanggan_check;

ALTER TABLE penjualan
    DROP CONSTRAINT penjualan_status_check,
    DROP CONSTRAINT penjualan_jenis_pembayaran_check,
    DROP CONSTRAINT penjualan_status_pembayaran_check;
