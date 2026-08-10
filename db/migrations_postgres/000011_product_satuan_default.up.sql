-- Dua hal yang dibutuhkan modul product, keduanya keputusan dari isu #1.

-- 1. Maksimal satu satuan default input per produk.
--
-- Tidak ada constraint apa pun untuk ini di migrasi 000002, jadi sebuah produk bisa
-- punya dua baris is_default_input = true dan tidak ada yang bisa memutuskan mana
-- yang dipakai saat input transaksi.
--
-- Ditegakkan indeks, bukan pengecekan di usecase: dua request bersamaan bisa lolos
-- pengecekan berdua lalu meninggalkan dua baris default. Partial index hanya
-- mengikat baris yang is_default_input, jadi berapa pun satuan non-default tetap
-- boleh. Pelanggarannya muncul sebagai SQLSTATE 23505 yang sudah dipetakan ke 409.
CREATE UNIQUE INDEX product_satuan_default_uidx
    ON product_satuan (id_product)
    WHERE is_default_input;

-- 2. Indeks pendukung ORDER BY nama, id.
--
-- product_nama_idx hanya pada (nama). Setiap endpoint list mengurutkan dengan
-- `ORDER BY nama, id` supaya paginasinya stabil, dan indeks satu kolom tidak
-- melayani pemecah serinya. Yang lama dibuang karena jadi awalan indeks baru ini,
-- sehingga sepenuhnya berlebihan.
DROP INDEX product_nama_idx;

CREATE INDEX product_nama_id_idx ON product (nama, id);
