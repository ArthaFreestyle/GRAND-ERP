DROP TRIGGER IF EXISTS kartu_stok_no_truncate ON kartu_stok;
DROP TRIGGER IF EXISTS kartu_stok_append_only ON kartu_stok;
DROP TRIGGER IF EXISTS kartu_stok_hitung_saldo ON kartu_stok;

DROP FUNCTION IF EXISTS kartu_stok_tolak_perubahan();
DROP FUNCTION IF EXISTS kartu_stok_hitung_saldo();
