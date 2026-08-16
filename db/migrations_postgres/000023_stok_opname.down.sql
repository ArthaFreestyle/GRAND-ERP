DROP INDEX IF EXISTS stok_opname_buka_id_idx;
DROP INDEX IF EXISTS stok_opname_ruang_terbuka_uidx;

ALTER TABLE stok_opname
    DROP CONSTRAINT IF EXISTS stok_opname_dibatalkan_oleh_fkey,
    DROP CONSTRAINT IF EXISTS stok_opname_status_check;

ALTER TABLE stok_opname
    DROP COLUMN IF EXISTS ts_batal,
    DROP COLUMN IF EXISTS alasan_batal,
    DROP COLUMN IF EXISTS dibatalkan_oleh;

-- Kembalikan badan fungsi ke versi 000017 apa adanya -- fungsi bisa dikembalikan,
-- tidak seperti nilai enum.
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
