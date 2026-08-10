-- Satuan lazim. Dibutuhkan modul product (#1): product.id_satuan_dasar dan
-- product_satuan tidak bisa diisi tanpa baris di sini.
--
-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
--
-- Target ON CONFLICT harus persis ekspresi indeksnya: sejak migrasi 000009
-- keunikan satuan ada pada lower(nama), dan ON CONFLICT (nama) tidak cocok
-- dengan indeks mana pun sehingga seeder gagal.
INSERT INTO satuan (nama, is_aktif) VALUES
    ('PCS',    TRUE),
    ('BOX',    TRUE),
    ('LUSIN',  TRUE),
    ('KARTON', TRUE),
    ('RIM',    TRUE)
ON CONFLICT (lower(nama)) DO NOTHING;
