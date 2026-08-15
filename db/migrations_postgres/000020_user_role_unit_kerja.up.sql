-- Isu #12 fase 3: wewenang bertempat. Sampai sekarang user_role menjawab "peran
-- apa"; sesudah ini satu baris grant juga menjawab "di unit mana", sehingga satu
-- orang bisa memegang peran berbeda di tempat berbeda — kasir di outlet A,
-- inventaris di outlet B — sebagai dua baris user_role yang terpisah.
--
-- Konteks aktif per sesi (klaim JWT, POST /auth/switch-context) adalah fase 4
-- dan TIDAK ada di migrasi ini. Lihat "Unit kerja and location-bound authority"
-- di CLAUDE.md.

ALTER TABLE user_role ADD COLUMN id_unit_kerja BIGINT;

-- NULL berarti berlaku di SELURUH unit — bentuk grant SUPERADMIN, dan bentuk
-- setiap grant yang ada sebelum migrasi ini (di-backfill NULL secara implisit
-- oleh ADD COLUMN tanpa DEFAULT). Sebuah flag terpisah is_lintas_unit ditolak
-- oleh isu ini: dua kolom yang harus selalu sepakat akhirnya tidak sepakat.
ALTER TABLE user_role
    ADD CONSTRAINT user_role_id_unit_kerja_fkey FOREIGN KEY (id_unit_kerja) REFERENCES unit_kerja (id);

-- user_role_user_role_uidx (migrasi 000002) sekarang salah bentuk: ia mengindeks
-- (user_id, role_id) telanjang, jadi begitu id_unit_kerja ada, "INVENTARIS di A"
-- dan "INVENTARIS di B" untuk user yang sama akan ditolak sebagai duplikat
-- padahal keduanya grant yang sah dan berbeda.
DROP INDEX user_role_user_role_uidx;

-- Unik per (user, role, unit). Tapi indeks unik biasa tidak membatasi NULL —
-- PostgreSQL menganggap NULL <> NULL — jadi indeks ini SENDIRIAN membiarkan
-- banyak baris "role X, unit manapun" (id_unit_kerja IS NULL) untuk pasangan
-- (user, role) yang sama disisipkan berkali-kali tanpa ditolak.
CREATE UNIQUE INDEX user_role_grant_uidx ON user_role (user_id, role_id, id_unit_kerja);

-- Indeks kedua yang menutup celah NULL di atas. Wajib, bukan pelengkap: tanpa
-- ini grant lintas-unit yang sama bisa disisipkan sepuluh kali untuk pasangan
-- yang sama, dan tidak ada yang menolaknya.
CREATE UNIQUE INDEX user_role_grant_global_uidx ON user_role (user_id, role_id) WHERE id_unit_kerja IS NULL;
