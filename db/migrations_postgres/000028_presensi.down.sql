-- Tidak ada yang menunjuk presensi, dan modul ini tidak menambah satu pun nilai
-- enum, jadi turunnya benar-benar bersih — tidak ada sisa yang tak bisa dihapus
-- seperti yang ditinggalkan setiap ALTER TYPE ... ADD VALUE.
DROP TRIGGER IF EXISTS presensi_set_updated_at ON presensi;
DROP TABLE presensi;
