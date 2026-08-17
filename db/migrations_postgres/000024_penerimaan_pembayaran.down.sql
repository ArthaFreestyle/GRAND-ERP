-- Kebalikan dari 000024.
--
-- Tabel penerimaan_pembayaran dan pembayaran_alokasi tidak ikut dibuang: keduanya
-- dibuat migrasi 000006, jadi yang ini hanya melepas apa yang ia tambahkan. Tidak
-- ada kolom pada penjualan untuk dilepas — isu ini tidak menambahkan satu pun,
-- lihat komentar di 000024_penerimaan_pembayaran.up.sql.

DROP INDEX pembayaran_alokasi_baris_uidx;

DROP INDEX penerimaan_pembayaran_status_giro_idx;

DROP INDEX penerimaan_pembayaran_tanggal_id_idx;

CREATE INDEX penerimaan_pembayaran_tanggal_idx ON penerimaan_pembayaran (tanggal);

ALTER TABLE penerimaan_pembayaran
    DROP CONSTRAINT penerimaan_pembayaran_tanggal_cair_check,
    DROP CONSTRAINT penerimaan_pembayaran_giro_check,
    DROP CONSTRAINT penerimaan_pembayaran_status_giro_nilai_check,
    DROP CONSTRAINT penerimaan_pembayaran_metode_check,
    DROP CONSTRAINT penerimaan_pembayaran_status_check;
