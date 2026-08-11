package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// KartuStokRepository owns every statement touching kartu_stok.
//
// There is no Update and no Delete, and there never will be: migration 000004
// installs triggers that raise on UPDATE, DELETE, and TRUNCATE. A correction is a
// new reversing row pointing at the original through id_kartu_stok_asal.
type KartuStokRepository struct{}

func NewKartuStokRepository() *KartuStokRepository {
	return &KartuStokRepository{}
}

// kartuStokReadColumns casts every NUMERIC to TEXT so the exact decimal survives.
// Scanning NUMERIC into a float64 would round inventory value on the way out, and
// these are the numbers the whole valuation rests on.
const kartuStokReadColumns = `id, id_barang, id_ruang, tanggal_transaksi, jenis_transaksi,
	stok_awal, stok_masuk, stok_keluar, stok_akhir,
	qty_input::TEXT, id_satuan_input,
	harga_pokok_satuan::TEXT, nilai_masuk::TEXT, nilai_keluar::TEXT, nilai_akhir::TEXT,
	ref_table, ref_id_transaksi, id_kartu_stok_asal, keterangan, created_by, created_at`

// Insert appends one movement.
//
// Note which columns the INSERT lists and which it does not. The application
// supplies the direction (stok_masuk OR stok_keluar, never both), nilai_masuk, and
// the reference columns — nothing else. stok_awal, stok_akhir, harga_pokok_satuan,
// nilai_keluar, and nilai_akhir are computed by the kartu_stok_hitung_saldo trigger
// and would be overwritten anyway; leaving them out of the statement is what stops
// a caller from believing otherwise.
//
// RETURNING reads them all back, so the caller sees the balance the database
// actually recorded rather than one it guessed.
//
// The trigger also takes a pg_advisory_xact_lock on (id_barang, id_ruang), so
// concurrent postings for the same product and room serialize instead of both
// reading the same running balance. It raises on negative stock and on posting into
// a periode with status TUTUP; both arrive as SQLSTATE 23514.
func (r *KartuStokRepository) Insert(ctx context.Context, db DBTX, kartu *entity.KartuStok) error {
	const query = `
		INSERT INTO kartu_stok (
			id_barang, id_ruang, tanggal_transaksi, jenis_transaksi,
			stok_masuk, stok_keluar, qty_input, id_satuan_input, nilai_masuk,
			ref_table, ref_id_transaksi, id_kartu_stok_asal, keterangan, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING ` + kartuStokReadColumns

	row := db.QueryRowContext(
		ctx, query,
		kartu.IDBarang, kartu.IDRuang, kartu.TanggalTransaksi, kartu.JenisTransaksi,
		kartu.StokMasuk, kartu.StokKeluar, kartu.QtyInput, kartu.IDSatuanInput,
		kartu.NilaiMasuk,
		kartu.RefTable, kartu.RefIDTransaksi, kartu.IDKartuStokAsal, kartu.Keterangan,
		kartu.CreatedBy,
	)

	if err := scanKartuStok(row, kartu); err != nil {
		return fmt.Errorf("insert kartu_stok: %w", err)
	}

	return nil
}

// FindByRef returns every movement a document produced, oldest first.
//
// This is how a cancellation finds the rows it has to reverse: kartu_stok_ref_idx
// covers (ref_table, ref_id_transaksi) exactly.
func (r *KartuStokRepository) FindByRef(ctx context.Context, db DBTX, refTable string, refID int64) ([]entity.KartuStok, error) {
	const query = `SELECT ` + kartuStokReadColumns + `
		FROM kartu_stok
		WHERE ref_table = $1 AND ref_id_transaksi = $2
		ORDER BY id`

	rows, err := db.QueryContext(ctx, query, refTable, refID)
	if err != nil {
		return nil, fmt.Errorf("select kartu_stok: %w", err)
	}
	defer rows.Close()

	list := make([]entity.KartuStok, 0, 8)

	for rows.Next() {
		var kartu entity.KartuStok

		if err := scanKartuStok(rows, &kartu); err != nil {
			return nil, fmt.Errorf("scan kartu_stok: %w", err)
		}

		list = append(list, kartu)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kartu_stok: %w", err)
	}

	return list, nil
}

// HasRef reports whether a document already produced movements.
//
// Posting twice would double the stock, and unlike a duplicated master row that
// cannot be undone — only reversed. The status guard on the document is the real
// defence; this is the one that does not depend on the status column being right.
func (r *KartuStokRepository) HasRef(ctx context.Context, db DBTX, refTable string, refID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM kartu_stok WHERE ref_table = $1 AND ref_id_transaksi = $2
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, refTable, refID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check kartu_stok ref: %w", err)
	}

	return exists, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so the column order lives
// in one place instead of drifting between the insert and the read.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanKartuStok(row rowScanner, kartu *entity.KartuStok) error {
	return row.Scan(
		&kartu.ID, &kartu.IDBarang, &kartu.IDRuang, &kartu.TanggalTransaksi,
		&kartu.JenisTransaksi,
		&kartu.StokAwal, &kartu.StokMasuk, &kartu.StokKeluar, &kartu.StokAkhir,
		&kartu.QtyInput, &kartu.IDSatuanInput,
		&kartu.HargaPokokSatuan, &kartu.NilaiMasuk, &kartu.NilaiKeluar, &kartu.NilaiAkhir,
		&kartu.RefTable, &kartu.RefIDTransaksi, &kartu.IDKartuStokAsal, &kartu.Keterangan,
		&kartu.CreatedBy, &kartu.CreatedAt,
	)
}
