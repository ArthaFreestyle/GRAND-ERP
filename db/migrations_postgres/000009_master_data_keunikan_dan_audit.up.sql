-- Keputusan bagian 7 dari isu #2, dijawab: keunikan kode tidak peka huruf,
-- ekspedisi.nama wajib unik, satuan dapat is_aktif, dan keempat tabel master
-- dapat jejak perubahan (updated_at + updated_by).

-- 1. Keunikan kode/nama tidak peka huruf besar-kecil.
--
-- UNIQUE (kode) biasa menganggap 'SUP-01' dan 'sup-01' dua kode berbeda, jadi
-- keduanya bisa masuk dan operator melihat dua supplier yang sama. Indeks pada
-- lower(kode) menutup celah itu.
--
-- Sifat NULL tidak berubah: lower(NULL) tetap NULL, dan indeks unik PostgreSQL
-- mengizinkan banyak baris NULL. Jadi master tanpa kode tetap boleh banyak.
ALTER TABLE supplier  DROP CONSTRAINT supplier_kode_key;
ALTER TABLE pelanggan DROP CONSTRAINT pelanggan_kode_key;
ALTER TABLE ruang     DROP CONSTRAINT ruang_kode_key;
ALTER TABLE satuan    DROP CONSTRAINT satuan_nama_key;

CREATE UNIQUE INDEX supplier_kode_lower_uidx  ON supplier  (lower(kode));
CREATE UNIQUE INDEX pelanggan_kode_lower_uidx ON pelanggan (lower(kode));
CREATE UNIQUE INDEX ruang_kode_lower_uidx     ON ruang     (lower(kode));
CREATE UNIQUE INDEX satuan_nama_lower_uidx    ON satuan    (lower(nama));

-- 2. ekspedisi.nama wajib unik. Tidak peka huruf, sama seperti di atas.
-- nama di sini NOT NULL, jadi tidak ada celah banyak-NULL.
CREATE UNIQUE INDEX ekspedisi_nama_lower_uidx ON ekspedisi (lower(nama));

-- 3. satuan.is_aktif — supaya satuan salah ketik bisa dipensiunkan. Menghapus
-- bukan pilihan: satuan dirujuk product_satuan dan seluruh *_detail.
ALTER TABLE satuan ADD COLUMN is_aktif BOOLEAN NOT NULL DEFAULT TRUE;

-- 4. Jejak perubahan data master.
--
-- created_at/created_by hanya ada di supplier dan pelanggan; satuan dan
-- ekspedisi belum punya sama sekali, jadi ditambahkan sekalian agar keempatnya
-- punya bentuk yang sama. Semua kolom *_by nullable dan diisi NULL sampai modul
-- autentikasi ada — itu yang membuat modul ini tidak terblokir olehnya.
ALTER TABLE satuan
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN created_by BIGINT,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

ALTER TABLE ekspedisi
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN created_by BIGINT,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

ALTER TABLE supplier
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

ALTER TABLE pelanggan
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_by BIGINT;

ALTER TABLE satuan
    ADD CONSTRAINT satuan_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT satuan_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE ekspedisi
    ADD CONSTRAINT ekspedisi_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE,
    ADD CONSTRAINT ekspedisi_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE supplier
    ADD CONSTRAINT supplier_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE pelanggan
    ADD CONSTRAINT pelanggan_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

-- updated_at diurus trigger, bukan aplikasi. Fungsinya sudah ada dari migrasi
-- 000001 dan dipakai ulang di sini.
CREATE TRIGGER satuan_set_updated_at
    BEFORE UPDATE ON satuan
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER ekspedisi_set_updated_at
    BEFORE UPDATE ON ekspedisi
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER supplier_set_updated_at
    BEFORE UPDATE ON supplier
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER pelanggan_set_updated_at
    BEFORE UPDATE ON pelanggan
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();

-- Pencarian daftar memakai ILIKE '%...%' pada nama. Indeks btree biasa tidak
-- membantu pola berawalan wildcard, tapi tetap dipasang untuk ORDER BY nama, id
-- yang dipakai setiap endpoint list.
CREATE INDEX supplier_nama_idx  ON supplier  (nama, id);
CREATE INDEX pelanggan_nama_idx ON pelanggan (nama, id);
CREATE INDEX ekspedisi_nama_idx ON ekspedisi (nama, id);
CREATE INDEX satuan_nama_idx    ON satuan    (nama, id);
