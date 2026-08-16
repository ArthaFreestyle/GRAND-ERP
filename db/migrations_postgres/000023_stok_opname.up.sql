-- Stok opname: snapshot cutoff, selisih hitung fisik, dan pembekuan ruang (isu #15).
--
-- Tabel stok_opname/stok_opname_detail sudah ada sejak 000007, dan SO_SURPLUS serta
-- SO_DEFISIT sudah ada di jenis_transaksi sejak 000002 -- jadi tidak ada ALTER TYPE
-- sama sekali di sini, seberuntung mutasi (000018) dan pemakaian (000021).
--
-- Yang ditambahkan di sini ada empat, dan yang pertama adalah yang benar-benar
-- mengubah proyek ini: kartu_stok_hitung_saldo() diganti supaya SETIAP insert ke
-- kartu_stok, dari modul mana pun, menolak posting ke ruang yang sedang diopname.
-- Ini dokumen keenam yang menulis kartu_stok dan satu-satunya yang, selama terbuka,
-- membekukan apa yang boleh dilakukan modul lain -- pola yang sama seperti periode
-- (000017), bukan pemeriksaan biasa di usecase.

-- ---------------------------------------------------------------------------
-- 1. Mesin saldo kartu stok, dengan pembekuan ruang
-- ---------------------------------------------------------------------------
--
-- Sama persis dengan versi 000017 kecuali satu blok baru: sesudah kunci periode dan
-- sebelum kunci (barang, ruang), trigger mengambil shared advisory lock atas ruang
-- transaksi itu lalu memeriksa apakah ruang itu sedang diopname (status DRAFT atau
-- DIAJUKAN). Urutan kuncinya seragam di seluruh proyek: periode -> ruang ->
-- (barang, ruang). Trigger sudah mengambil yang pertama dan ketiga; yang kedua
-- disisipkan di antaranya, di tempat yang sama pada setiap insert, jadi tidak ada
-- siklus baru.
--
-- Kenapa dibaca dari status, bukan ts_verified IS NULL: keduanya kelihatannya
-- menjawab pertanyaan yang sama ("belum diverifikasi, jadi beku"), tapi
-- ts_verified salah di dua arah -- BATAL sebelum diverifikasi tetap NULL (beku
-- selamanya, tanpa jalan keluar), dan DITOLAK lalu kembali ke DRAFT sudah terisi
-- (bebas padahal hitungannya belum beres). status IN ('DRAFT', 'DIAJUKAN')
-- menjawab "belum selesai", yang justru dimaksud.
--
-- Pengecualian: baris yang ditulis stok_opname itu sendiri (ref_table = 'stok_opname'
-- dan ref_id_transaksi = id opname yang sedang membekukan ruang itu) selalu lolos --
-- kalau tidak, opname tidak akan pernah bisa memposting penyesuaiannya sendiri ke
-- ruang yang justru sedang dibekukannya.
--
-- RAISE memakai ERRCODE 55000 (object_not_in_prerequisite_state), bukan
-- check_violation. invalidOnCheck sudah menanggung dua arti (stok kurang dan
-- periode tutup); menambah arti ketiga membuat setiap pesan di call site menjadi
-- "salah satu dari tiga hal ini". Kode SQLSTATE tersendiri berarti funnel
-- tersendiri (conflictOnRuangBeku) yang menjawab 409 -- status yang lebih tepat
-- daripada 400: request-nya tidak salah, keadaannya yang belum siap.
CREATE OR REPLACE FUNCTION kartu_stok_hitung_saldo()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    prev_stok      BIGINT;
    prev_nilai     NUMERIC(20, 2);
    prev_hpp       NUMERIC(20, 4);
    thn            INT := EXTRACT(YEAR FROM NEW.tanggal_transaksi)::INT;
    bln            INT := EXTRACT(MONTH FROM NEW.tanggal_transaksi)::INT;
    status_periode VARCHAR;
    opname_id      BIGINT;
    opname_nomor   VARCHAR;
BEGIN
    -- Kunci periode. Sama persis dengan 000017.
    PERFORM pg_advisory_xact_lock_shared(
        hashtextextended('periode:' || thn::TEXT || '-' || bln::TEXT, 0)
    );

    SELECT p.status INTO status_periode
    FROM periode p
    WHERE p.tahun = thn AND p.bulan = bln;

    IF status_periode = 'TUTUP' THEN
        RAISE EXCEPTION 'periode %-% sudah TUTUP; transaksi tidak dapat diposting', thn, bln
            USING ERRCODE = 'check_violation';
    END IF;

    -- Kunci ruang. Ekspresi kuncinya harus sama persis dengan
    -- repository.ruangLockKey -- kalau berbeda, keduanya mengunci dua hal yang
    -- berlainan dan tidak ada yang menahan siapa pun.
    PERFORM pg_advisory_xact_lock_shared(
        hashtextextended('ruang:' || NEW.id_ruang::TEXT, 0)
    );

    -- Ruang yang sedang diopname (DRAFT/DIAJUKAN) menolak setiap posting kecuali
    -- dari opname itu sendiri. stok_opname_ruang_terbuka_uidx menjamin paling
    -- banyak satu baris terbuka per ruang, jadi SELECT ini paling banyak satu baris.
    SELECT so.idstok_opname, so.nomor INTO opname_id, opname_nomor
    FROM stok_opname so
    WHERE so.id_ruang = NEW.id_ruang AND so.status IN ('DRAFT', 'DIAJUKAN');

    IF opname_id IS NOT NULL
       AND NOT (NEW.ref_table = 'stok_opname' AND NEW.ref_id_transaksi = opname_id) THEN
        RAISE EXCEPTION
            'ruang % sedang diopname (nomor %); transaksi tidak dapat diposting',
            NEW.id_ruang, opname_nomor
            USING ERRCODE = '55000';
    END IF;

    -- Kunci (barang, ruang). Sama persis dengan 000017.
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.id_barang::TEXT || ':' || NEW.id_ruang::TEXT, 0)
    );

    NEW.stok_masuk  := COALESCE(NEW.stok_masuk, 0);
    NEW.stok_keluar := COALESCE(NEW.stok_keluar, 0);
    NEW.nilai_masuk := COALESCE(NEW.nilai_masuk, 0);

    SELECT ks.stok_akhir, ks.nilai_akhir, ks.harga_pokok_satuan
    INTO prev_stok, prev_nilai, prev_hpp
    FROM kartu_stok ks
    WHERE ks.id_barang = NEW.id_barang
      AND ks.id_ruang = NEW.id_ruang
    ORDER BY ks.id DESC
    LIMIT 1;

    prev_stok  := COALESCE(prev_stok, 0);
    prev_nilai := COALESCE(prev_nilai, 0);
    prev_hpp   := COALESCE(prev_hpp, 0);

    NEW.stok_awal  := prev_stok;
    NEW.stok_akhir := prev_stok + NEW.stok_masuk - NEW.stok_keluar;

    IF NEW.stok_akhir < 0 THEN
        RAISE EXCEPTION
            'stok tidak mencukupi: barang %, ruang %, saldo %, diminta keluar %',
            NEW.id_barang, NEW.id_ruang, prev_stok, NEW.stok_keluar
            USING ERRCODE = 'check_violation';
    END IF;

    IF NEW.stok_masuk > 0 THEN
        NEW.nilai_keluar := 0;
        NEW.nilai_akhir  := prev_nilai + NEW.nilai_masuk;
        NEW.harga_pokok_satuan := CASE
            WHEN NEW.stok_akhir > 0 THEN ROUND(NEW.nilai_akhir / NEW.stok_akhir, 4)
            ELSE prev_hpp
        END;
    ELSE
        NEW.nilai_masuk        := 0;
        NEW.harga_pokok_satuan := prev_hpp;
        NEW.nilai_keluar       := ROUND(NEW.stok_keluar * prev_hpp, 2);
        NEW.nilai_akhir        := prev_nilai - NEW.nilai_keluar;
    END IF;

    IF NEW.stok_akhir = 0 THEN
        NEW.nilai_akhir := 0;
    END IF;

    IF NEW.nilai_akhir < 0 THEN
        RAISE EXCEPTION
            'nilai persediaan negatif: barang %, ruang %, nilai %',
            NEW.id_barang, NEW.id_ruang, NEW.nilai_akhir
            USING ERRCODE = 'check_violation';
    END IF;

    NEW.created_at := COALESCE(NEW.created_at, now());

    RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------------------
-- 2. Alur status
-- ---------------------------------------------------------------------------
--
-- DRAFT -> DIAJUKAN -> POSTED -> BATAL, plus DIAJUKAN -> DRAFT (tolak). Mengikuti
-- pembelian, bukan pemakaian: penolakan di sini berarti "hitung ulang, angkanya
-- tidak masuk akal" -- koreksi kertas, bukan keputusan bisnis -- dan sejak ada
-- pembekuan, status terminal yang tidak melepas beku adalah ruang mati.
--
-- Belum ada CHECK sama sekali hari ini, jadi kosakatanya masih bebas dipilih --
-- dipilih sekarang yang sama dengan lima modul lain, bukan nanti seperti yang
-- terpaksa dilakukan 000021 untuk pemakaian.
ALTER TABLE stok_opname
    ADD COLUMN dibatalkan_oleh BIGINT,
    ADD COLUMN alasan_batal    TEXT,
    ADD COLUMN ts_batal        TIMESTAMPTZ;

ALTER TABLE stok_opname
    ADD CONSTRAINT stok_opname_status_check
        CHECK (status IN ('DRAFT', 'DIAJUKAN', 'POSTED', 'BATAL')),
    ADD CONSTRAINT stok_opname_dibatalkan_oleh_fkey
        FOREIGN KEY (dibatalkan_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

-- ---------------------------------------------------------------------------
-- 3. Satu opname terbuka per ruang
-- ---------------------------------------------------------------------------
--
-- Dua opname terbuka di ruang yang sama berarti dua snapshot cutoff atas rak yang
-- sama, dan keduanya akan memposting selisihnya masing-masing -- koreksi yang sama
-- dibukukan dua kali. Tidak bisa digantikan pemeriksaan di Go: dua request
-- bersamaan sama-sama lolos pengecekan sebelum salah satunya menulis.
--
-- Sejak pembekuan ada, index ini juga yang membuat "opname mana yang membekukan
-- ruang ini" punya jawaban tunggal -- itu yang dibaca trigger di atas.
CREATE UNIQUE INDEX stok_opname_ruang_terbuka_uidx
    ON stok_opname (id_ruang) WHERE status IN ('DRAFT', 'DIAJUKAN');

-- ---------------------------------------------------------------------------
-- 4. Urutan baca daftar dokumen
-- ---------------------------------------------------------------------------
--
-- Mendukung ORDER BY tgl_buka DESC, idstok_opname DESC pada endpoint list.
-- stok_opname_ruang_buka_idx yang ada (000007) menaik dan tidak menopangnya, sama
-- seperti mutasi_tanggal_idx tidak menopang mutasi_tanggal_id_idx di 000018.
CREATE INDEX stok_opname_buka_id_idx ON stok_opname (tgl_buka DESC, idstok_opname DESC);
