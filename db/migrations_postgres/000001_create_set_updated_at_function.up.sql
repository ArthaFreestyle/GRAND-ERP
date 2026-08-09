-- Menjaga updated_at tanpa bergantung pada layer aplikasi. Dipakai tabel mana
-- pun yang punya kolom updated_at (lihat trigger di 000002).
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
