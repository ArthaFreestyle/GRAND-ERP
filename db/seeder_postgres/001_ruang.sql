-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
--
-- Target ON CONFLICT mengikuti indeks yang ada: sejak migrasi 000009 keunikan
-- kode ruang pindah ke lower(kode), dan ON CONFLICT (kode) tidak lagi cocok
-- dengan indeks mana pun.
INSERT INTO ruang (kode, nama_ruang, is_aktif) VALUES
    ('GD-UTM', 'Gudang Utama',      TRUE),
    ('GD-TRN', 'Gudang Transit',    TRUE),
    ('TK-01',  'Toko Cabang 1',     TRUE),
    ('TK-02',  'Toko Cabang 2',     TRUE),
    ('GD-RSK', 'Gudang Barang Rusak', TRUE)
ON CONFLICT (lower(kode)) DO NOTHING;
