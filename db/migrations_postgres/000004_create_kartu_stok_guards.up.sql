-- Mesin saldo kartu stok. Semua kolom saldo dan nilai dihitung di sini, bukan
-- di aplikasi: apa pun yang dikirim klien untuk stok_awal, stok_akhir,
-- harga_pokok_satuan, nilai_keluar, dan nilai_akhir akan ditimpa. Aplikasi
-- hanya mengisi arah pergerakan (stok_masuk / stok_keluar) dan nilai_masuk.
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
BEGIN
    -- Serialisasi penulisan untuk (barang, ruang) yang sama. Tanpa ini dua
    -- transaksi paralel bisa membaca saldo terakhir yang sama dan menghasilkan
    -- dua baris dengan stok_awal identik. Lock dilepas otomatis saat commit.
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.id_barang::TEXT || ':' || NEW.id_ruang::TEXT, 0)
    );

    -- Periode tertutup menolak posting. Periode yang belum pernah dibuat
    -- dianggap terbuka, supaya database baru tidak macet sebelum ada data.
    SELECT p.status INTO status_periode
    FROM periode p
    WHERE p.tahun = thn AND p.bulan = bln;

    IF status_periode = 'TUTUP' THEN
        RAISE EXCEPTION 'periode %-% sudah TUTUP; transaksi tidak dapat diposting', thn, bln
            USING ERRCODE = 'check_violation';
    END IF;

    NEW.stok_masuk  := COALESCE(NEW.stok_masuk, 0);
    NEW.stok_keluar := COALESCE(NEW.stok_keluar, 0);
    NEW.nilai_masuk := COALESCE(NEW.nilai_masuk, 0);

    -- Saldo terakhir dibaca berdasarkan id, bukan tanggal: urutan pencatatan
    -- yang menentukan rantai saldo, bukan urutan kejadian.
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
        -- Barang masuk menggeser rata-rata bergerak.
        NEW.nilai_keluar := 0;
        NEW.nilai_akhir  := prev_nilai + NEW.nilai_masuk;
        NEW.harga_pokok_satuan := CASE
            WHEN NEW.stok_akhir > 0 THEN ROUND(NEW.nilai_akhir / NEW.stok_akhir, 4)
            ELSE prev_hpp
        END;
    ELSE
        -- Barang keluar tidak pernah mengubah harga pokok.
        NEW.nilai_masuk        := 0;
        NEW.harga_pokok_satuan := prev_hpp;
        NEW.nilai_keluar       := ROUND(NEW.stok_keluar * prev_hpp, 2);
        NEW.nilai_akhir        := prev_nilai - NEW.nilai_keluar;
    END IF;

    -- Stok habis wajib bernilai nol. Tanpa ini, sisa pembulatan rata-rata
    -- bergerak menumpuk jadi rupiah hantu di gudang yang kosong.
    IF NEW.stok_akhir = 0 THEN
        NEW.nilai_akhir := 0;
    END IF;

    -- nilai_akhir negatif berarti rantai saldo sudah rusak; hentikan di sini
    -- daripada menyimpan angka yang mustahil.
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

CREATE TRIGGER kartu_stok_hitung_saldo
    BEFORE INSERT ON kartu_stok
    FOR EACH ROW
    EXECUTE FUNCTION kartu_stok_hitung_saldo();

-- Append-only ditegakkan database, bukan sekadar konvensi tim. Koreksi
-- dilakukan dengan baris pembalik yang mengisi id_kartu_stok_asal.
CREATE OR REPLACE FUNCTION kartu_stok_tolak_perubahan()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION
        'kartu_stok bersifat append-only: % ditolak. Buat baris pembalik dengan id_kartu_stok_asal.',
        TG_OP
        USING ERRCODE = 'restrict_violation';
    RETURN NULL;
END;
$$;

CREATE TRIGGER kartu_stok_append_only
    BEFORE UPDATE OR DELETE ON kartu_stok
    FOR EACH ROW
    EXECUTE FUNCTION kartu_stok_tolak_perubahan();

-- TRUNCATE melewati trigger baris, jadi butuh penjaga tingkat statement.
CREATE TRIGGER kartu_stok_no_truncate
    BEFORE TRUNCATE ON kartu_stok
    FOR EACH STATEMENT
    EXECUTE FUNCTION kartu_stok_tolak_perubahan();
