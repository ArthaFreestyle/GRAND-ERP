-- Kebalikan dari 000030.
--
-- Arah ini membuang data: setiap catatan mengapa dan atas dasar apa nilai
-- persediaan awal diciptakan hilang, sementara baris kartu_stok yang dihasilkannya
-- tetap ada dan tidak bisa dihapus. Setelah turun, stok itu tidak lagi punya
-- dokumen yang menjelaskannya. Jangan jalankan di database yang sudah punya
-- saldo_awal POSTED.

DROP TABLE saldo_awal_detail;

DROP TABLE saldo_awal;

-- Nilai enum 'SALDO_AWAL' sengaja ditinggalkan.
--
-- PostgreSQL tidak punya ALTER TYPE ... DROP VALUE. Membuangnya berarti membuat
-- ulang seluruh tipe enum beserta setiap kolom yang memakainya, dan kolom itu ada
-- di kartu_stok yang append-only serta dijaga trigger -- prosedur yang jauh lebih
-- berbahaya daripada masalah yang dipecahkannya. Nilai enum yang menganggur tidak
-- membebani apa pun, dan baris kartu_stok yang terlanjur memakainya tetap harus
-- bisa dibaca (preseden 000013).
