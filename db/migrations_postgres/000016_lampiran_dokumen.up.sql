-- Subsistem lampiran berkas (isu #5): unggah, sajikan, tempel, bersihkan.
--
-- Dipicu oleh #4 -- faktur supplier berupa kertas dan perlu difoto di meja
-- penerimaan -- tetapi kebutuhannya lintas modul: retur butuh foto barang rusak,
-- penjualan butuh surat jalan bertanda tangan, stok opname butuh berita acara.
-- Karena itu ini satu tabel infrastruktur, bukan kolom di salah satu dokumen.
--
-- Ini juga tabel kedua di proyek ini yang barisnya boleh hilang, setelah
-- user_role. Bedanya: penghapusan di sini **lunak**. Barisnya tetap ada dengan
-- deleted_at terisi, yang berkasnya sudah tidak ada lagi di disk. Jejak bahwa
-- pernah ada unggahan lebih berharga daripada satu baris yang dihemat, dan itu
-- pula yang membuat cronjob bisa idempoten.

CREATE TABLE dokumen (
    id              BIGSERIAL PRIMARY KEY,

    -- Nama dari klien. **Hanya untuk ditampilkan** dan tidak pernah menyentuh
    -- filesystem: tanpa pemisahan ini, `../../config.json` menjadi path yang sah.
    nama_asli       VARCHAR(255)    NOT NULL,

    -- Nama yang dihasilkan server: UUID + ekstensi yang diturunkan dari MIME hasil
    -- sniffing isi berkas. Bare filename, tanpa direktori -- direktori akarnya
    -- adalah konfigurasi (dokumen.storage_path), bukan data, supaya pindah disk
    -- tidak berarti menulis ulang setiap baris.
    path_simpan     VARCHAR(255)    NOT NULL,

    -- Hasil deteksi isi berkas, bukan header Content-Type dari klien. Header itu
    -- sepenuhnya dikendalikan penyerang; isi berkas tidak.
    mime            VARCHAR(128)    NOT NULL,
    ukuran_byte     BIGINT          NOT NULL,

    -- Opsional, dan gunanya mendeteksi faktur yang sama diunggah dua kali. Tidak
    -- dipakai sebagai kunci: dua berkas identik yang sengaja ditempel ke dua
    -- dokumen berbeda tetap dua lampiran yang sah.
    checksum_sha256 CHAR(64),

    -- Referensi polimorfik, mengikuti pola yang sudah dipakai kartu_stok
    -- (ref_table + ref_id_transaksi). Tidak ada foreign key -- justru itu
    -- maksudnya: satu tabel lampiran melayani setiap jenis dokumen tanpa perlu
    -- satu kolom per modul.
    --
    -- **NULL-nya disengaja.** Foto diambil sebelum dokumennya tersimpan: petugas
    -- memotret faktur sambil membongkar box, dan dokumen pembeliannya belum tentu
    -- sudah dibuat. Jadi alurnya unggah dulu, tempel kemudian, dan di antara
    -- keduanya baris ini yatim. Yang membuat berkas yatim mungkin ada adalah
    -- kolom ini, dan yang membuatnya gampang dicari juga kolom ini.
    ref_table       VARCHAR(64),
    ref_id          BIGINT,

    created_by      BIGINT          NOT NULL REFERENCES users (id),
    -- Dasar penentuan umur berkas yatim, jadi bukan sekadar kolom audit.
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now(),

    -- Soft delete. Terisi berarti berkasnya sudah dihapus dari disk, entah oleh
    -- operator lewat DELETE atau oleh pembersihan yatim di worker.
    deleted_at      TIMESTAMPTZ,

    -- Berkas kosong tidak pernah merupakan lampiran yang berarti, dan ia satu-satunya
    -- ukuran yang lolos setiap batas.
    CONSTRAINT dokumen_ukuran_byte_check CHECK (ukuran_byte > 0),

    -- Setengah tertempel tidak punya arti: ref_table tanpa ref_id menunjuk seluruh
    -- tabel, ref_id tanpa ref_table menunjuk entah ke mana. Ini pula yang membuat
    -- "yatim" bisa diperiksa dengan satu kolom saja.
    CONSTRAINT dokumen_ref_check CHECK ((ref_table IS NULL) = (ref_id IS NULL))
);

-- Nama simpan dihasilkan dari UUID, jadi tabrakan praktis mustahil. Indeks ini ada
-- untuk yang tidak praktis: dua baris menunjuk satu berkas berarti menghapus yang
-- satu membuat yang lain menunjuk berkas yang tidak ada.
CREATE UNIQUE INDEX dokumen_path_simpan_uidx ON dokumen (path_simpan);

-- Mengambil lampiran satu dokumen. Parsial atas deleted_at karena layar dokumen
-- tidak pernah menampilkan lampiran yang berkasnya sudah hilang.
CREATE INDEX dokumen_ref_idx
    ON dokumen (ref_table, ref_id)
    WHERE deleted_at IS NULL;

-- Inilah yang dipindai cronjob pembersihan, dan ia tetap kecil apa pun besarnya
-- tabel: isinya hanya baris yang belum tertempel dan belum dihapus. Indeks penuh
-- atas created_at akan tumbuh seiring seluruh riwayat lampiran, padahal yang
-- ditanyakan cuma satu himpunan yang seharusnya nyaris selalu kosong.
CREATE INDEX dokumen_yatim_idx
    ON dokumen (created_at)
    WHERE ref_id IS NULL AND deleted_at IS NULL;

-- Mendeteksi unggahan ganda faktur yang sama. Parsial karena checksum opsional,
-- dan baris terhapus bukan kandidat duplikat -- berkasnya sudah tidak ada.
CREATE INDEX dokumen_checksum_idx
    ON dokumen (checksum_sha256)
    WHERE checksum_sha256 IS NOT NULL AND deleted_at IS NULL;

-- Tidak ada updated_at, jadi tidak ada trigger set_updated_at. Baris ini hanya
-- berubah dua kali seumur hidupnya -- saat ditempel dan saat dihapus -- dan
-- keduanya sudah punya kolom yang mencatat waktunya sendiri.
--
-- Whitelist ref_table sengaja **tidak** ditaruh sebagai CHECK. Nilai yang sah
-- adalah kebijakan aplikasi (repository.RefTableDokumen), dan menaruhnya di sini
-- berarti setiap modul baru yang butuh lampiran menuntut satu migrasi hanya untuk
-- menambah satu string. Yang menegakkannya adalah usecase, yang juga memeriksa
-- barisnya benar-benar ada -- sesuatu yang CHECK tidak bisa lakukan.
--
-- Whitelist MIME juga tidak ada di sini, dengan alasan yang sama: ia batas yang
-- akan dilonggarkan (mungkin TIFF dari scanner, mungkin HEIC dari iPhone) tanpa
-- perlu menyentuh skema.
