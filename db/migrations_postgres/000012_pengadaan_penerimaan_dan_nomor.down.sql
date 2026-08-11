-- Kebalikan dari 000012.
--
-- Arah ini membuang data, bukan sekadar melonggarkan aturan: qty_diterima,
-- keterangan selisih, dan seluruh jejak persetujuan hilang. Dokumen POSTED yang
-- barangnya datang sebagian akan kehilangan satu-satunya catatan berapa yang
-- benar-benar datang, sementara baris kartu_stok-nya tetap ada dan tidak bisa
-- dihapus. Jangan jalankan ini di database yang sudah punya pembelian POSTED.
--
-- pembelian.status juga bisa berisi 'DIAJUKAN' atau 'BATAL', yang setelah turun
-- tidak lagi punya arti di kode mana pun.

DROP INDEX pembelian_tanggal_id_idx;

CREATE INDEX pembelian_tanggal_idx ON pembelian (tanggal);

DROP INDEX pembelian_supplier_faktur_uidx;

DROP INDEX pembelian_status_penerimaan_idx;

ALTER TABLE pembelian
    DROP CONSTRAINT pembelian_disetujui_oleh_fkey,
    DROP CONSTRAINT pembelian_diajukan_oleh_fkey,
    DROP CONSTRAINT pembelian_metode_alokasi_angkut_check,
    DROP CONSTRAINT pembelian_jenis_pembayaran_check,
    DROP CONSTRAINT pembelian_status_pembayaran_check,
    DROP CONSTRAINT pembelian_status_penerimaan_check,
    DROP CONSTRAINT pembelian_status_check;

ALTER TABLE pembelian
    DROP COLUMN status_penerimaan,
    DROP COLUMN alasan_tolak,
    DROP COLUMN disetujui_pada,
    DROP COLUMN disetujui_oleh,
    DROP COLUMN diajukan_pada,
    DROP COLUMN diajukan_oleh;

ALTER TABLE pembelian_detail
    DROP CONSTRAINT pembelian_detail_qty_diterima_dasar_check,
    DROP CONSTRAINT pembelian_detail_qty_diterima_check,
    DROP CONSTRAINT pembelian_detail_qty_faktur_check;

ALTER TABLE pembelian_detail
    DROP COLUMN keterangan_selisih,
    DROP COLUMN qty_diterima_dasar,
    DROP COLUMN qty_diterima;

ALTER TABLE pembelian_detail
    RENAME COLUMN qty_faktur TO qty_input;

DROP TABLE document_counter;
