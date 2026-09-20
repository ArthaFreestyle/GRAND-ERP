-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
--
-- Bisnis ini hanya punya satu unit kerja: GRAND FC Sandana. Migrasi 000019
-- menyeed unit bawaan 'PUSAT' / 'Unit Utama' semata agar ruang punya sesuatu
-- untuk ditunjuk; nama itu diganti di sini alih-alih menambah unit baru. Guard
-- `nama = 'Unit Utama'` membuat UPDATE ini aman dijalankan berulang tanpa
-- menimpa balik nama yang sudah diubah lewat PATCH /unit_kerja.
UPDATE unit_kerja SET nama = 'GRAND FC Sandana'
WHERE kode = 'PUSAT' AND nama = 'Unit Utama';

-- Target ON CONFLICT mengikuti indeks yang ada: sejak migrasi 000009 keunikan
-- kode ruang pindah ke lower(kode), dan ON CONFLICT (kode) tidak lagi cocok
-- dengan indeks mana pun.
--
-- id_unit_kerja dicari lewat kode 'PUSAT', bukan angka tetap: migrasi 000019
-- yang membuat unit bawaan itu tidak menjanjikan id berapa yang akan didapat.
--
-- Hanya dua ruang yang benar-benar dipakai, satu toko dan satu gudang, keduanya
-- di bawah unit yang sama. Katalog barang (product) tidak diseed per ruang dan
-- tidak perlu: master data itu satu tabel yang sama untuk seluruh ruang, dan
-- stok tiap ruang lahir dari dokumen (pembelian, mutasi, stok_opname) lewat
-- kartu_stok, bukan dari seeder ini — lihat catatan di kepala 005_product.sql.
INSERT INTO ruang (kode, nama_ruang, is_aktif, id_unit_kerja) VALUES
    ('TK-SANDANA', 'Toko Depan Pasar Sandana', TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT')),
    ('GD-SUBUR',   'Gudang Kampung Subur',     TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT'))
ON CONFLICT (lower(kode)) DO NOTHING;
