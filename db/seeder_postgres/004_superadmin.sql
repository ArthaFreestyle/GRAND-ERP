-- User pertama, supaya API bisa dipakai sama sekali.
--
-- Sejak otorisasi berbasis role dipasang, POST /api/v1/user hanya boleh diakses
-- SUPERADMIN. Tanpa baris di sini tidak ada satu pun user yang bisa login, jadi tidak
-- ada yang bisa membuat user pertama — API terkunci total dari dirinya sendiri.
--
-- Butuh 003_role.sql sudah jalan, karena SUPERADMIN dicari berdasarkan nama.

-- ============================================================================
-- PERINGATAN: password di bawah adalah "admin12345" dan tercatat di repositori
-- publik ini. Ini kredensial untuk mesin sendiri, BUKAN untuk lingkungan yang
-- bisa dijangkau orang lain.
--
-- Ganti segera setelah login pertama:
--
--   PATCH /api/v1/user/{id}  {"password": "..."}
--
-- Atau nonaktifkan akunnya setelah membuat superadmin sungguhan:
--
--   PATCH /api/v1/user/{id}  {"is_aktif": false}
-- ============================================================================

-- Hash bcrypt cost 10 dari "admin12345". Ditulis literal, bukan dihitung di SQL:
-- PostgreSQL tidak bisa membuat hash bcrypt tanpa ekstensi pgcrypto, dan menambah
-- ekstensi hanya untuk seeder tidak sebanding.
INSERT INTO users (username, nama_lengkap, password, is_aktif) VALUES
    ('admin', 'Administrator Bawaan', '$2a$10$N8Hwq4QGxq0sbctMsp4deenfobdkeDQnhOuUqhgoLYTrS5.Twis02', TRUE)
ON CONFLICT (lower(username)) DO NOTHING;

-- Pemberian role SUPERADMIN. Keduanya dicari berdasarkan nama supaya seeder tidak
-- bergantung pada id yang kebetulan terbentuk — kolom id-nya IDENTITY, jadi nilainya
-- tidak bisa dipastikan dari sini.
--
-- created_by dibiarkan NULL: tidak ada pelaku yang memberi role ini, ia lahir dari
-- seeder.
INSERT INTO user_role (user_id, role_id)
SELECT u.id, r.id
FROM users u
CROSS JOIN role r
WHERE lower(u.username) = 'admin'
  AND lower(r.nama) = 'superadmin'
ON CONFLICT (user_id, role_id) DO NOTHING;
