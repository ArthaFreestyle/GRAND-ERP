-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
--
-- Target ON CONFLICT mengikuti indeks yang ada: sejak migrasi 000009 keunikan
-- kode ruang pindah ke lower(kode), dan ON CONFLICT (kode) tidak lagi cocok
-- dengan indeks mana pun.
--
-- id_unit_kerja dicari lewat kode 'PUSAT', bukan angka tetap: migrasi 000019
-- yang membuat unit bawaan itu tidak menjanjikan id berapa yang akan didapat.
INSERT INTO ruang (kode, nama_ruang, is_aktif, id_unit_kerja) VALUES
    ('GD-UTM', 'Gudang Utama',      TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT')),
    ('GD-TRN', 'Gudang Transit',    TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT')),
    ('TK-01',  'Toko Cabang 1',     TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT')),
    ('TK-02',  'Toko Cabang 2',     TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT')),
    ('GD-RSK', 'Gudang Barang Rusak', TRUE, (SELECT id FROM unit_kerja WHERE kode = 'PUSAT'))
ON CONFLICT (lower(kode)) DO NOTHING;
