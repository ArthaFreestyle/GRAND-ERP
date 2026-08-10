-- Tiga role yang berlaku sekarang. Tanpa baris di sini, user_role tidak bisa
-- diisi sama sekali dan setiap user berakhir tanpa role.
--
-- Nama ditulis KAPITAL mengikuti nilai enum lain di skema ini (jenis_transaksi,
-- status 'BUKA'/'TUTUP'). Keunikan role.nama tidak peka huruf sejak migrasi
-- 000010, jadi 'CASHIER' dan 'cashier' bertabrakan — kapitalisasi di sini soal
-- konsistensi, bukan pencegah duplikat.
--
-- Nama role akan dipakai pengecekan izin saat middleware otorisasi dibangun.
-- Endpoint PATCH /api/v1/role/{id} bisa mengganti nama, jadi menggantinya berarti
-- memutus pengecekan yang mengacu nama lama. Pensiunkan dengan is_aktif = false
-- dan buat role baru, jangan ganti nama role yang sudah dipakai.
--
-- Idempoten: aman dijalankan ulang pada database yang sudah terisi.
INSERT INTO role (nama, is_aktif) VALUES
    ('SUPERADMIN', TRUE),
    ('CASHIER',    TRUE),
    ('INVENTARIS', TRUE)
ON CONFLICT (lower(nama)) DO NOTHING;
