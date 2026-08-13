-- Tutup buku bulanan (isu #6).
--
-- Tabel periode sudah ada sejak 000002 dan trigger kartu_stok sudah menghormatinya
-- sejak 000004. Yang ditambahkan di sini dua hal, dan keduanya lahir dari keputusan
-- yang selama ini belum pernah ditulis di mana pun:
--
--   1. Jejak pembukaan kembali. ditutup_oleh/ts_tutup hanya mencatat penutupan
--      terakhir; membuka lalu menutup lagi menimpanya tanpa sisa, sehingga tidak ada
--      yang tahu bahwa bulan itu sempat dibuka.
--   2. Kunci yang membuat "menutup" dan "memposting" tidak bisa saling menyalip.

-- Siapa yang membuka kembali, dan kapan. Sepasang kolom, bukan tabel jejak
-- tersendiri: yang perlu dijawab adalah "apakah bulan ini pernah dibuka setelah
-- ditutup, dan oleh siapa", dan sepasang kolom sudah menjawabnya. Riwayat lengkap
-- setiap penutupan adalah pertanyaan yang berbeda, dan tabelnya bisa ditambahkan
-- kalau memang ditanyakan.
ALTER TABLE periode
    ADD COLUMN dibuka_oleh BIGINT,
    ADD COLUMN ts_buka     TIMESTAMPTZ;

ALTER TABLE periode
    ADD CONSTRAINT periode_dibuka_oleh_fkey FOREIGN KEY (dibuka_oleh) REFERENCES users (id) DEFERRABLE INITIALLY IMMEDIATE;

-- Mesin saldo kartu stok, sama persis dengan 000004 kecuali blok periodenya.
--
-- Yang berubah: sebelum membaca status periode, trigger mengambil advisory lock
-- **shared** atas (tahun, bulan) transaksi itu. Penutupan buku mengambil lock yang
-- sama dalam mode eksklusif (repository.PeriodeRepository.Lock).
--
-- Tanpa ini ada jendela yang bukan khayalan: di READ COMMITTED sebuah transaksi
-- posting bisa membaca 'BUKA', lalu penutupan commit, lalu postingnya commit -- dan
-- baris itu mendarat di bulan yang menurut bukunya sudah tertutup. Tutup buku
-- biasanya dijalankan sore hari, persis saat orang masih menginput.
--
-- Kenapa advisory lock dan bukan SELECT ... FOR SHARE atas barisnya: bulan yang
-- belum pernah ditutup **tidak punya baris** -- itu keputusan 000004, supaya
-- database baru tidak macet sebelum ada data -- dan menutup bulan berarti MEMBUAT
-- barisnya. Jadi justru pada penutupan pertama sebuah bulan, satu-satunya kasus yang
-- benar-benar sering terjadi, tidak ada apa pun untuk dikunci. Advisory lock tidak
-- butuh baris.
--
-- Lock periode diambil lebih dulu daripada lock (barang, ruang), dan urutannya
-- seragam untuk setiap penulis, jadi tidak ada jalan menuju deadlock. Sesama posting
-- sama-sama shared sehingga tidak pernah saling menunggu; yang menunggu hanya
-- penutupan buku, dan memang itu maunya.
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
    -- Kunci periode. Ruang kunci ini dibagi dengan lock (barang, ruang) di bawah,
    -- jadi awalan 'periode:' bukan hiasan -- itu yang memisahkan keduanya. Tabrakan
    -- hash 64-bit tetap mungkin secara teori, dan akibatnya cuma dua penulis yang
    -- tidak berhubungan saling menunggu sebentar, bukan hasil yang salah.
    --
    -- Ekspresi kuncinya harus sama persis dengan yang dipakai
    -- repository.PeriodeRepository -- kalau berbeda, keduanya mengunci dua hal yang
    -- berlainan dan tidak ada yang menahan siapa pun.
    PERFORM pg_advisory_xact_lock_shared(
        hashtextextended('periode:' || thn::TEXT || '-' || bln::TEXT, 0)
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

    -- Serialisasi penulisan untuk (barang, ruang) yang sama. Tanpa ini dua
    -- transaksi paralel bisa membaca saldo terakhir yang sama dan menghasilkan
    -- dua baris dengan stok_awal identik. Lock dilepas otomatis saat commit.
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.id_barang::TEXT || ':' || NEW.id_ruang::TEXT, 0)
    );

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

-- Melayani GET /periode yang difilter tahun dan status, dan sekaligus urutannya.
-- Berakhir di bulan, yang unik bersama tahun -- pasangan itulah identitas sebenarnya
-- sebuah periode, seperti yang sudah dinyatakan periode_tahun_bulan_uidx.
CREATE INDEX periode_tahun_bulan_idx ON periode (tahun DESC, bulan DESC);
