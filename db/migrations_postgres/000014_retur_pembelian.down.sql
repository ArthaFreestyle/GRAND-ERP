-- Kebalikan dari 000014.
--
-- Tabelnya tidak ikut dibuang: retur_pembelian dan retur_pembelian_detail dibuat
-- migrasi 000005, jadi yang ini hanya melepas apa yang ia tambahkan.
--
-- Arah ini tetap membuang data: setiap catatan siapa yang mengajukan, menyetujui,
-- dan membatalkan sebuah retur hilang, sementara baris kartu_stok yang dihasilkan
-- dokumen itu tetap ada dan tidak bisa dihapus. Dokumen berstatus BATAL juga
-- kehilangan alasan pembatalannya, sehingga pembalikannya tidak lagi bisa
-- dibedakan dari kekeliruan. Jangan jalankan di database yang sudah punya
-- retur_pembelian POSTED atau BATAL.

ALTER TABLE retur_pembelian_detail
    DROP CONSTRAINT retur_pembelian_detail_nilai_check;

DROP INDEX retur_pembelian_detail_baris_uidx;

DROP INDEX retur_pembelian_tanggal_id_idx;

CREATE INDEX retur_pembelian_tanggal_idx ON retur_pembelian (tanggal);

DROP INDEX retur_pembelian_status_idx;

ALTER TABLE retur_pembelian
    DROP CONSTRAINT retur_pembelian_dibatalkan_oleh_fkey,
    DROP CONSTRAINT retur_pembelian_disetujui_oleh_fkey,
    DROP CONSTRAINT retur_pembelian_diajukan_oleh_fkey,
    DROP CONSTRAINT retur_pembelian_total_check,
    DROP CONSTRAINT retur_pembelian_status_check;

ALTER TABLE retur_pembelian
    DROP COLUMN alasan_tolak,
    DROP COLUMN alasan_batal,
    DROP COLUMN dibatalkan_oleh,
    DROP COLUMN disetujui_pada,
    DROP COLUMN disetujui_oleh,
    DROP COLUMN diajukan_pada,
    DROP COLUMN diajukan_oleh;
