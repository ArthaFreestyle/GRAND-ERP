-- Satu user boleh punya banyak role. Tabel users, role, dan user_role sudah ada
-- dari migrasi 000002, tapi bentuknya menghalangi persyaratan itu. Migrasi ini
-- membetulkannya dan menyamakan role/users dengan pola master data 000009.

-- 1. role_active dibuang.
--
-- Ini bug, bukan sekadar kolom berlebih. UNIQUE (role_active) berlaku untuk
-- seluruh tabel, jadi begitu satu user memakai baris user_role tertentu sebagai
-- role aktifnya, tidak ada user lain yang boleh punya role aktif dari baris yang
-- sama — dan karena user_role.id unik per pasangan (user, role), efek praktisnya
-- satu sistem hanya bisa punya satu kasir aktif. Kasir kedua ditolak database.
--
-- FK-nya juga menunjuk user_role (id) tanpa menyertakan user_id, jadi tidak ada
-- yang mencegah role aktif user A menunjuk baris penugasan milik user B.
--
-- Keputusan: user_role satu-satunya sumber kebenaran kepemilikan role. Izin
-- dihitung dari gabungan seluruh role yang dipegang user, tanpa konsep "role yang
-- sedang aktif", sehingga kolom ini tidak punya penerus.
ALTER TABLE users DROP CONSTRAINT users_role_active_fkey;
ALTER TABLE users DROP CONSTRAINT users_role_active_key;
ALTER TABLE users DROP COLUMN role_active;

-- 2. Keunikan tidak peka huruf besar-kecil, alasannya sama seperti migrasi 000009:
-- UNIQUE biasa menganggap 'Budi' dan 'budi' dua username berbeda, dan dua akun
-- yang hanya beda kapital adalah jebakan saat login nanti dibangun.
--
-- email nullable, dan lower(NULL) tetap NULL, jadi banyak user tanpa email tetap
-- boleh — sifat yang sama seperti kode master yang NULL.
ALTER TABLE users DROP CONSTRAINT users_username_key;
ALTER TABLE users DROP CONSTRAINT users_email_key;
ALTER TABLE role  DROP CONSTRAINT role_nama_key;

CREATE UNIQUE INDEX users_username_lower_uidx ON users (lower(username));
CREATE UNIQUE INDEX users_email_lower_uidx    ON users (lower(email));
CREATE UNIQUE INDEX role_nama_lower_uidx      ON role  (lower(nama));

-- 3. role dapat is_aktif dan jejak perubahan.
--
-- Menghapus role bukan pilihan: user_role merujuknya, dan menghapus role yang
-- pernah dipakai berarti kehilangan catatan siapa dulu boleh apa. Role usang
-- dipensiunkan dengan is_aktif = false, sama seperti seluruh master data.
ALTER TABLE role
    ADD COLUMN is_aktif   BOOLEAN     NOT NULL DEFAULT TRUE,
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN created_by BIGINT,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

-- 4. users dapat kolom pelaku. created_at/updated_at sudah ada dari 000002.
ALTER TABLE users
    ADD COLUMN created_by BIGINT,
    ADD COLUMN updated_by BIGINT;

-- 5. user_role dapat jejak pemberian: kapan role diberikan dan oleh siapa.
--
-- Mencabut role tetap DELETE, bukan is_aktif. user_role adalah tabel jembatan
-- yang tidak dirujuk tabel transaksi mana pun (satu-satunya yang pernah
-- menunjuknya adalah users.role_active, yang baru dibuang di atas), jadi
-- menghapus baris di sini tidak memutus foreign key dan tidak menghapus jejak
-- dokumen apa pun — created_by di dokumen menunjuk users, bukan user_role.
ALTER TABLE user_role
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN created_by BIGINT;

-- Semua kolom *_by nullable dan diisi NULL sampai modul autentikasi ada — itu
-- yang membuat modul ini tidak terblokir olehnya. users_created_by_fkey menunjuk
-- users sendiri; superadmin pertama lahir dari seeder dengan created_by NULL.
ALTER TABLE role
    ADD CONSTRAINT role_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT role_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE users
    ADD CONSTRAINT users_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT users_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE user_role
    ADD CONSTRAINT user_role_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

-- updated_at diurus trigger. Fungsinya dari migrasi 000001; users sudah punya
-- triggernya sejak 000002, role belum.
CREATE TRIGGER role_set_updated_at
    BEFORE UPDATE ON role
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Menopang ORDER BY nama/username, id yang dipakai setiap endpoint list.
CREATE INDEX users_username_idx ON users (username, id);
CREATE INDEX role_nama_idx      ON role  (nama, id);

-- user_role_user_role_uidx (user_id, role_id) sudah melayani arah "role apa saja
-- milik user ini". Indeks ini melayani arah sebaliknya, "user mana saja yang
-- punya role ini", yang dipakai filter role_id di GET /api/v1/user.
CREATE INDEX user_role_role_id_idx ON user_role (role_id, user_id);
