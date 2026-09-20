-- Katalog produk per unit kerja. Sampai sekarang product itu master global dan
-- satu-satunya kaitannya ke unit kerja adalah stok: kartu_stok dipartisi per
-- (id_barang, id_ruang) dan ruang.id_unit_kerja NOT NULL. Tabel ini menjawab
-- pertanyaan lain — "produk ini BOLEH diperdagangkan di unit mana" — sebelum ada
-- satu pun stok. product tetap satu baris global (kode_barang, satuan, harga jual
-- tidak berubah); yang per-unit hanya keanggotaannya.
--
-- Tabel join tanpa is_aktif, sama seperti user_role: tidak ada dokumen yang
-- menunjuk baris ini (dokumen menunjuk product dan ruang), jadi mencabut
-- keanggotaan tidak memutus foreign key dan tidak menghapus jejak apa pun.
-- Alasan produk keluar dari sebuah unit dijaga di aplikasi (masih ada stok),
-- bukan di sini.
CREATE TABLE product_unit_kerja (
    id_product    BIGINT      NOT NULL,
    id_unit_kerja BIGINT      NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by    BIGINT,

    CONSTRAINT product_unit_kerja_pkey PRIMARY KEY (id_product, id_unit_kerja),
    CONSTRAINT product_unit_kerja_id_product_fkey FOREIGN KEY (id_product) REFERENCES product (id),
    CONSTRAINT product_unit_kerja_id_unit_kerja_fkey FOREIGN KEY (id_unit_kerja) REFERENCES unit_kerja (id),
    CONSTRAINT product_unit_kerja_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id)
        DEFERRABLE INITIALLY IMMEDIATE
);

-- Primary key sudah melayani "unit mana saja yang memuat produk ini"; indeks ini
-- untuk arah sebaliknya, "produk apa saja di unit ini", yang dipakai daftar katalog.
CREATE INDEX product_unit_kerja_unit_idx ON product_unit_kerja (id_unit_kerja, id_product);

-- Backfill: setiap produk yang sudah ada masuk ke setiap unit yang AKTIF, supaya
-- perilaku hari ini tidak berubah untuk database yang sudah berisi dokumen.
--
-- Ditambah setiap unit yang sudah memegang baris kartu_stok produk itu, walau
-- unitnya kini nonaktif. Tanpa itu, stok yang nyata akan berada di unit yang
-- katalognya tidak memuat produknya, dan invarian yang dijaga aplikasi ("produk
-- yang masih punya stok di sebuah unit tidak bisa keluar dari katalognya") sudah
-- dilanggar sejak hari pertama. UNION menghapus baris ganda antara dua sumber.
INSERT INTO product_unit_kerja (id_product, id_unit_kerja)
SELECT p.id, u.id
FROM product p
CROSS JOIN unit_kerja u
WHERE u.is_aktif
UNION
SELECT DISTINCT ks.id_barang, r.id_unit_kerja
FROM kartu_stok ks
JOIN ruang r ON r.id = ks.id_ruang;
