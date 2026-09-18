-- Kebalikan dari 000027: constraint dulu, kolom terakhir.
--
-- Nota yang sudah terlanjur memungut PPN kehilangan rincian pajaknya di sini,
-- sementara total-nya tetap memuat angka itu. Turun setelah ada nota ber-PPN
-- berarti menerima total yang tidak bisa lagi dipecah jadi DPP dan pajaknya.
ALTER TABLE penjualan
    DROP CONSTRAINT penjualan_ppn_check;

ALTER TABLE penjualan
    DROP COLUMN ppn;
