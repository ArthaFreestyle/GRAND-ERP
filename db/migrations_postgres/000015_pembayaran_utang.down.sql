-- Kebalikan dari 000015.
--
-- Tabel pembayaran_utang dan pembayaran_utang_alokasi tidak ikut dibuang: keduanya
-- dibuat migrasi 000008, jadi yang ini hanya melepas apa yang ia tambahkan.
--
-- Arah ini membuang data: nilai_kredit_utang setiap retur hilang, dan angka itu adalah
-- snapshot yang dibekukan saat posting -- menghitungnya ulang setelah naik kembali akan
-- memakai pembelian.total dan pembelian_detail.subtotal apa adanya saat itu, yang untuk
-- dokumen POSTED memang tidak berubah, tetapi tidak ada lagi yang membuktikannya.
--
-- pembelian.status_pembayaran juga tidak dihitung ulang. Faktur yang tercatat LUNAS
-- berkat kredit retur akan tetap tertulis LUNAS padahal angka pendukungnya sudah tidak
-- ada. Jangan jalankan di database yang sudah punya pembayaran_utang POSTED.

DROP INDEX pembayaran_utang_alokasi_baris_uidx;

DROP INDEX pembayaran_utang_status_giro_idx;

DROP INDEX pembayaran_utang_tanggal_id_idx;

CREATE INDEX pembayaran_utang_tanggal_idx ON pembayaran_utang (tanggal);

ALTER TABLE pembayaran_utang
    DROP CONSTRAINT pembayaran_utang_tanggal_cair_check,
    DROP CONSTRAINT pembayaran_utang_giro_check,
    DROP CONSTRAINT pembayaran_utang_status_giro_nilai_check,
    DROP CONSTRAINT pembayaran_utang_metode_check,
    DROP CONSTRAINT pembayaran_utang_status_check;

ALTER TABLE retur_pembelian
    DROP CONSTRAINT retur_pembelian_nilai_kredit_utang_check;

ALTER TABLE retur_pembelian
    DROP COLUMN nilai_kredit_utang;
