-- Kebalikan dari 000013.
--
-- Arah ini membuang data: setiap catatan kapan barang susulan benar-benar datang
-- hilang, sementara baris kartu_stok yang dihasilkannya tetap ada dan tidak bisa
-- dihapus. Setelah turun, stok itu tidak lagi punya dokumen yang menjelaskannya.
-- Jangan jalankan di database yang sudah punya penerimaan_susulan POSTED.
--
-- pembelian.status_penerimaan juga tidak ikut dihitung ulang: baris yang sudah
-- lengkap berkat susulan akan tetap tertulis LENGKAP padahal sumber datanya sudah
-- tidak ada.

DROP TABLE penerimaan_susulan_detail;

DROP TABLE penerimaan_susulan;

-- Nilai enum 'PENERIMAAN_SUSULAN' sengaja ditinggalkan.
--
-- PostgreSQL tidak punya ALTER TYPE ... DROP VALUE. Membuangnya berarti membuat
-- ulang seluruh tipe enum beserta setiap kolom yang memakainya, dan kolom itu ada
-- di kartu_stok yang append-only serta dijaga trigger -- prosedur yang jauh lebih
-- berbahaya daripada masalah yang dipecahkannya. Nilai enum yang menganggur tidak
-- membebani apa pun.
--
-- Baris kartu_stok yang terlanjur memakai nilai itu juga tetap ada, dan itulah
-- alasan sebenarnya: membuang nilainya akan membuat baris-baris itu mustahil
-- dibaca.
