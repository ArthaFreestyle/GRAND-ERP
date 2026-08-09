DROP TABLE IF EXISTS pembayaran_utang_alokasi CASCADE;
DROP TABLE IF EXISTS pembayaran_utang CASCADE;

ALTER TABLE pembelian_detail
    DROP CONSTRAINT IF EXISTS pembelian_detail_jumlah_koli_check;
ALTER TABLE pembelian_detail
    DROP COLUMN IF EXISTS jumlah_koli;

DROP INDEX IF EXISTS pembelian_status_pembayaran_idx;

ALTER TABLE pembelian
    ALTER COLUMN metode_alokasi_angkut SET DEFAULT 'QTY';
ALTER TABLE pembelian
    DROP COLUMN IF EXISTS status_pembayaran;
ALTER TABLE pembelian
    DROP COLUMN IF EXISTS jenis_pembayaran;
