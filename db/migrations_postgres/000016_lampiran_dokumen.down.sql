-- Kebalikan dari 000016.
--
-- Ini membuang seluruh metadata lampiran, dan itu tidak simetris dengan apa yang
-- ada di disk: berkasnya ditulis ke dokumen.storage_path dan tidak ikut terhapus.
-- Setelah tabelnya hilang, tidak ada lagi yang tahu berkas mana milik dokumen
-- mana, dan pembersihan yatim di worker kehilangan satu-satunya daftar yang boleh
-- ia percaya -- ia menghapus berdasarkan baris, tidak pernah dengan memindai
-- direktori.
--
-- Jadi setelah menjalankan ini, isi direktori penyimpanan menjadi sampah yang
-- harus dibereskan sendiri.

DROP TABLE dokumen;
