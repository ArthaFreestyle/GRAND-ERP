-- Kebalikan dari 000010. Urutannya terbalik dari up: indeks dan trigger dulu,
-- kolom terakhir, role_active dipasang ulang paling akhir.
--
-- Perhatikan bahwa turun mengembalikan bug yang dibetulkan up: UNIQUE
-- (role_active) berarti kembali hanya satu user yang bisa punya role aktif.
-- Itu memang isi migrasi 000002, dan tugas file ini memulihkan keadaan itu apa
-- adanya, bukan memperbaikinya.
--
-- Turun bisa gagal di dua tempat, keduanya karena data yang tidak muat di bentuk
-- lama: username atau email yang hanya beda kapital (indeks lower(...) lebih
-- ketat daripada UNIQUE biasa, jadi arah ini justru longgar dan aman), dan role
-- yang sudah dipensiunkan is_aktif = false — kolomnya hilang, statusnya lenyap
-- tanpa kabar. Cabut penugasan role usang lebih dulu kalau itu penting.

DROP INDEX user_role_role_id_idx;
DROP INDEX role_nama_idx;
DROP INDEX users_username_idx;

DROP TRIGGER role_set_updated_at ON role;

ALTER TABLE user_role DROP CONSTRAINT user_role_created_by_fkey;

ALTER TABLE users
    DROP CONSTRAINT users_created_by_fkey,
    DROP CONSTRAINT users_updated_by_fkey;

ALTER TABLE role
    DROP CONSTRAINT role_created_by_fkey,
    DROP CONSTRAINT role_updated_by_fkey;

ALTER TABLE user_role
    DROP COLUMN created_at,
    DROP COLUMN created_by;

ALTER TABLE users
    DROP COLUMN created_by,
    DROP COLUMN updated_by;

ALTER TABLE role
    DROP COLUMN is_aktif,
    DROP COLUMN created_at,
    DROP COLUMN created_by,
    DROP COLUMN updated_at,
    DROP COLUMN updated_by;

DROP INDEX role_nama_lower_uidx;
DROP INDEX users_email_lower_uidx;
DROP INDEX users_username_lower_uidx;

ALTER TABLE role  ADD CONSTRAINT role_nama_key      UNIQUE (nama);
ALTER TABLE users ADD CONSTRAINT users_email_key    UNIQUE (email);
ALTER TABLE users ADD CONSTRAINT users_username_key UNIQUE (username);

ALTER TABLE users ADD COLUMN role_active BIGINT;

ALTER TABLE users
    ADD CONSTRAINT users_role_active_key  UNIQUE (role_active),
    ADD CONSTRAINT users_role_active_fkey FOREIGN KEY (role_active) REFERENCES user_role (id) DEFERRABLE INITIALLY IMMEDIATE;
