-- Kebalikan dari 000011.
--
-- Turun melonggarkan aturan: setelah ini sebuah produk boleh punya lebih dari satu
-- satuan default lagi. Data yang lolos indeks yang lebih ketat pasti lolos keadaan
-- tanpa indeks, jadi arah ini tidak bisa gagal karena data. Arah naik yang bisa —
-- kalau sudah ada produk dengan dua baris is_default_input, duplikatnya harus
-- dibereskan lebih dulu.

DROP INDEX product_nama_id_idx;

CREATE INDEX product_nama_idx ON product (nama);

DROP INDEX product_satuan_default_uidx;
