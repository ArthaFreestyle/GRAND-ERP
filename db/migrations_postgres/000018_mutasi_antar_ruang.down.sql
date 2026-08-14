-- Kebalikan dari 000018.
--
-- Tabelnya tidak ikut dibuang: mutasi dan mutasi_detail dibuat migrasi 000007,
-- jadi yang ini hanya melepas apa yang ia tambahkan.
--
-- Arah ini tetap membuang data: catatan siapa yang membatalkan sebuah mutasi dan
-- kenapa akan hilang, sementara baris kartu_stok yang dihasilkan dokumen itu
-- tetap ada dan tidak bisa dihapus -- append-only berlaku ke dua arah. Dokumen
-- berstatus BATAL jadi tidak bisa lagi dibedakan dari kekeliruan. Jangan
-- jalankan di database yang sudah punya mutasi POSTED atau BATAL.
--
-- mutasi_status_check ikut dilepas, jadi sesudah ini kolom status kembali VARCHAR
-- bebas. Baris berstatus 'POSTED' dan 'BATAL' tetap tinggal apa adanya.

DROP INDEX mutasi_tanggal_id_idx;

CREATE INDEX mutasi_tanggal_idx ON mutasi (tanggal);

DROP INDEX mutasi_status_idx;

ALTER TABLE mutasi
    DROP CONSTRAINT mutasi_dibatalkan_oleh_fkey,
    DROP CONSTRAINT mutasi_status_check;

ALTER TABLE mutasi
    DROP COLUMN alasan_batal,
    DROP COLUMN dibatalkan_oleh;
