-- Kebalikan dari 000021.
--
-- Tabelnya tidak ikut dibuang: pemakaian dan pemakaian_detail dibuat migrasi
-- 000007, jadi yang ini hanya melepas apa yang ia tambahkan.
--
-- Jangan jalankan di database yang sudah punya baris pemakaian berstatus POSTED
-- atau BATAL -- CHECK versi lama tidak mengenal nilai itu dan menolak baris yang
-- sudah ada.

DROP INDEX pemakaian_tanggal_id_idx;

CREATE INDEX pemakaian_tanggal_idx ON pemakaian (tanggal);

ALTER TABLE pemakaian
    DROP CONSTRAINT pemakaian_status_check;

ALTER TABLE pemakaian
    ADD CONSTRAINT pemakaian_status_check CHECK (
        status IN ('DRAFT', 'DIAJUKAN', 'DISETUJUI', 'DITOLAK', 'DIPOSTING', 'DIBATALKAN')
    );
