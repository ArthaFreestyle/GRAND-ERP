-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
INSERT INTO ruang (kode, nama_ruang, is_aktif) VALUES
    ('GD-UTM', 'Gudang Utama',      TRUE),
    ('GD-TRN', 'Gudang Transit',    TRUE),
    ('TK-01',  'Toko Cabang 1',     TRUE),
    ('TK-02',  'Toko Cabang 2',     TRUE),
    ('GD-RSK', 'Gudang Barang Rusak', TRUE)
ON CONFLICT (kode) DO NOTHING;
