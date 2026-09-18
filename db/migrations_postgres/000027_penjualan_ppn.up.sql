-- PPN keluaran di nota penjualan. Sisi pembelian sudah memilikinya sejak migrasi
-- 000005; sisi penjualan tidak, sehingga kasir tidak punya tempat menaruh pajak
-- yang dipungut dari pelanggan.
--
-- Bentuknya cermin pembelian.ppn: satu kolom nominal NUMERIC(20, 2), diketik
-- pemanggil, bukan persentase yang dihitung server. Tarif tinggal di klien —
-- persis seperti nota supplier yang tarifnya juga datang dari luar sistem ini —
-- dan yang dibekukan di dokumen adalah rupiahnya, angka yang tidak berubah kalau
-- tarif nasional berubah tahun depan. Setiap angka uang di proyek ini adalah
-- snapshot.
--
-- PPN bersifat exclusive: total = subtotal - diskon_nota + ppn + pembulatan,
-- rumus yang sama persis dengan pembelian. Harga di product_harga_jual adalah
-- DPP-nya, dan piutang pelanggan ikut naik sebesar PPN — memang itu yang ditagih.
--
-- TIDAK ADA pasangan ppn_dikreditkan seperti di pembelian, dan itu bukan kelalaian:
-- di sisi pembelian bendera itu memutuskan apakah PPN masukan ikut jadi harga
-- pokok persediaan. PPN keluaran tidak pernah punya pilihan itu — ia utang ke
-- negara, bukan biaya, tidak pernah menyentuh HPP, dan karena itu tidak pernah
-- menyentuh kartu_stok. Kolom bendera di sini cuma akan jadi pertanyaan yang
-- jawabannya selalu sama.
ALTER TABLE penjualan
    ADD COLUMN ppn NUMERIC(20, 2) NOT NULL DEFAULT 0;

-- Penjaga nilai, sebentuk dengan yang ditambahkan migrasi 000015 dan 000024 ke
-- kedua tabel pembayaran. Usecase menolaknya lebih dulu supaya pesannya menyebut
-- nama field; CHECK ini jaring terakhir kalau ada jalan tulis lain.
ALTER TABLE penjualan
    ADD CONSTRAINT penjualan_ppn_check CHECK (ppn >= 0);
