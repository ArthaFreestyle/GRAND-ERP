DROP INDEX document_counter_global_uidx;
DROP INDEX document_counter_scoped_uidx;

-- Fails if two different units have issued the same (prefix, tahun, bulan)
-- series since this migration went up — recreating the old
-- (prefix, tahun, bulan) uniqueness is only possible once that data no longer
-- exists. That is the down migration correctly refusing to silently collapse
-- two outlets' real series into one.
ALTER TABLE document_counter ADD CONSTRAINT document_counter_pkey PRIMARY KEY (prefix, tahun, bulan);

ALTER TABLE document_counter DROP COLUMN id_unit_kerja;
