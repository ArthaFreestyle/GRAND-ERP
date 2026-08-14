package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

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

// SaldoKey identifies one balance chain: kartu_stok is partitioned by
// (id_barang, id_ruang) and every running total is per pair.
//
// Same role FaktorKey plays for product_satuan — a map key for a batch read, so a
// document with many lines costs one query instead of one per line.
type SaldoKey struct {
	IDBarang int64
	IDRuang  int64
}

// saldoColumns reads the tail of a balance chain. The two money columns go out as
// TEXT for the reason kartuStokReadColumns does: NUMERIC through a float64 loses the
// exact decimal, and this is the inventory valuation.
const saldoColumns = `ks.id_barang, ks.id_ruang, ks.stok_akhir,
	ks.nilai_akhir::TEXT, ks.harga_pokok_satuan::TEXT,
	ks.tanggal_transaksi, ks.created_at`

// SaldoTerakhir reads what one product currently holds in one room.
//
// A pair with no movements at all answers a zero balance rather than sql.ErrNoRows.
// That is not a convenience: it is the same reading kartu_stok_hitung_saldo takes when
// it COALESCEs a missing previous row to zero, and a caller forced to distinguish
// "never moved" from "moved down to nothing" would have to reimplement that decision.
//
// Ordered by id, not by date — the chain is ordered by the sequence rows were
// recorded in, which is what the trigger does too. kartu_stok_saldo_idx covers
// (id_barang, id_ruang, id) exactly, so this is one index descent.
//
// This is a read and never a guard. Whatever it returns may already be stale by the
// time the caller acts on it; the trigger decides the balance under an advisory lock.
func (r *KartuStokRepository) SaldoTerakhir(ctx context.Context, db DBTX, idBarang, idRuang int64) (*entity.SaldoStok, error) {
	const query = `SELECT ` + saldoColumns + `
		FROM kartu_stok ks
		WHERE ks.id_barang = $1 AND ks.id_ruang = $2
		ORDER BY ks.id DESC
		LIMIT 1`

	saldo := &entity.SaldoStok{
		IDBarang:         idBarang,
		IDRuang:          idRuang,
		NilaiAkhir:       "0.00",
		HargaPokokSatuan: "0.0000",
	}

	err := db.QueryRowContext(ctx, query, idBarang, idRuang).Scan(
		&saldo.IDBarang, &saldo.IDRuang, &saldo.StokAkhir,
		&saldo.NilaiAkhir, &saldo.HargaPokokSatuan,
		&saldo.TanggalTransaksi, &saldo.TerakhirDiperbarui,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return saldo, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select saldo kartu_stok: %w", err)
	}

	return saldo, nil
}

// SaldoBatch reads the balance of many (product, room) pairs at once.
//
// One query, not one per line, following FindFaktorBatch: a document can carry
// hundreds of lines and every one of them wants its room's balance before it can say
// anything useful about a shortfall. Asking per line would be a round trip per row
// inside a transaction already holding row locks.
//
// Pairs that have never moved are simply absent from the map. The caller reads a
// missing key as a zero balance — the same reading SaldoTerakhir makes explicit.
//
// DISTINCT ON is what reduces each chain to its last row, and it forces the ORDER BY
// to lead with the partition columns. pgx/stdlib passes a Go []int64 through
// database/sql untouched, so the arrays need no wrapper.
func (r *KartuStokRepository) SaldoBatch(ctx context.Context, db DBTX, barangIDs, ruangIDs []int64) (map[SaldoKey]entity.SaldoStok, error) {
	const query = `
		SELECT DISTINCT ON (ks.id_barang, ks.id_ruang) ` + saldoColumns + `
		FROM kartu_stok ks
		JOIN (
			SELECT DISTINCT id_barang, id_ruang
			FROM unnest($1::BIGINT[], $2::BIGINT[]) AS pasangan(id_barang, id_ruang)
		) p ON p.id_barang = ks.id_barang AND p.id_ruang = ks.id_ruang
		ORDER BY ks.id_barang, ks.id_ruang, ks.id DESC`

	rows, err := db.QueryContext(ctx, query, barangIDs, ruangIDs)
	if err != nil {
		return nil, fmt.Errorf("select saldo kartu_stok batch: %w", err)
	}
	defer rows.Close()

	saldo := make(map[SaldoKey]entity.SaldoStok, len(barangIDs))

	for rows.Next() {
		var baris entity.SaldoStok

		if err := rows.Scan(
			&baris.IDBarang, &baris.IDRuang, &baris.StokAkhir,
			&baris.NilaiAkhir, &baris.HargaPokokSatuan,
			&baris.TanggalTransaksi, &baris.TerakhirDiperbarui,
		); err != nil {
			return nil, fmt.Errorf("scan saldo kartu_stok batch: %w", err)
		}

		saldo[SaldoKey{IDBarang: baris.IDBarang, IDRuang: baris.IDRuang}] = baris
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saldo kartu_stok batch: %w", err)
	}

	return saldo, nil
}

// SaldoPerRuang breaks one product's stock down by room, backing
// GET /api/v1/product/{id}/stok.
//
// Only rooms the product has actually moved through appear. A room it has never been
// in holds none of it, and listing every room in the master table would bury the two
// that matter under a page of zeros — while a room that emptied out still appears,
// with zero, because that is a fact about where the goods went.
//
// Ordered by room name so the list reads the way the rooms are named, ending in
// id_ruang, which is unique across the subquery precisely because DISTINCT ON made it
// so.
func (r *KartuStokRepository) SaldoPerRuang(ctx context.Context, db DBTX, idBarang int64) ([]entity.SaldoStok, error) {
	const query = `
		SELECT id_barang, id_ruang, nama_ruang, stok_akhir, nilai_akhir,
			harga_pokok_satuan, tanggal_transaksi, created_at
		FROM (
			SELECT DISTINCT ON (ks.id_ruang) ` + saldoColumns + `, r.nama_ruang
			FROM kartu_stok ks
			JOIN ruang r ON r.id = ks.id_ruang
			WHERE ks.id_barang = $1
			ORDER BY ks.id_ruang, ks.id DESC
		) saldo
		ORDER BY nama_ruang, id_ruang`

	rows, err := db.QueryContext(ctx, query, idBarang)
	if err != nil {
		return nil, fmt.Errorf("select saldo per ruang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.SaldoStok, 0, 8)

	for rows.Next() {
		var saldo entity.SaldoStok

		if err := rows.Scan(
			&saldo.IDBarang, &saldo.IDRuang, &saldo.NamaRuang, &saldo.StokAkhir,
			&saldo.NilaiAkhir, &saldo.HargaPokokSatuan,
			&saldo.TanggalTransaksi, &saldo.TerakhirDiperbarui,
		); err != nil {
			return nil, fmt.Errorf("scan saldo per ruang: %w", err)
		}

		list = append(list, saldo)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saldo per ruang: %w", err)
	}

	return list, nil
}

// KunciSaldo takes the balance locks for a whole document up front, in one canonical
// order, and must be called inside a transaction.
//
// This exists for exactly one document type so far, and it is not an optimisation. A
// mutasi takes two (id_barang, id_ruang) locks for the same product, and which one it
// takes first is decided by which way the goods are going:
//
//	dokumen A (gudang -> toko) : lock (X, gudang) then (X, toko)
//	dokumen B (toko -> gudang) : lock (X, toko)   then (X, gudang)
//
// Textbook ABBA. Until mutasi existed it could not happen, because every document
// touched one room. Taking every lock the document will need before the first insert,
// sorted by (id_barang, id_ruang), makes the cycle impossible rather than merely rare.
//
// The keys are sorted and deduplicated in Go and the SQL sorts them again behind an
// OFFSET 0 fence, which stops the planner from pulling the subquery up and evaluating
// pg_advisory_xact_lock in whatever order the join happened to produce. Two ways of
// saying the same thing, because getting it wrong reintroduces exactly the deadlock
// this is here to remove.
//
// The key expression must hash the same string kartu_stok_hitung_saldo does
// (migration 000004), or the trigger and this take two different locks and neither
// waits for the other.
//
// Note what this does NOT lock: the periode. The trigger takes that one first, and a
// caller pre-locking balances has to take the periode lock before this, keeping the
// global order periode -> (barang, ruang) that every writer follows. See
// PeriodeRepository.LockShared.
func (r *KartuStokRepository) KunciSaldo(ctx context.Context, db DBTX, keys []SaldoKey) error {
	if len(keys) == 0 {
		return nil
	}

	urut := append([]SaldoKey(nil), keys...)
	sort.Slice(urut, func(i, j int) bool {
		if urut[i].IDBarang != urut[j].IDBarang {
			return urut[i].IDBarang < urut[j].IDBarang
		}

		return urut[i].IDRuang < urut[j].IDRuang
	})

	barangIDs := make([]int64, 0, len(urut))
	ruangIDs := make([]int64, 0, len(urut))

	for i, key := range urut {
		if i > 0 && key == urut[i-1] {
			continue
		}

		barangIDs = append(barangIDs, key.IDBarang)
		ruangIDs = append(ruangIDs, key.IDRuang)
	}

	const query = `
		SELECT pg_advisory_xact_lock(kunci)
		FROM (
			SELECT hashtextextended(
				pasangan.id_barang::TEXT || ':' || pasangan.id_ruang::TEXT, 0
			) AS kunci
			FROM unnest($1::BIGINT[], $2::BIGINT[]) AS pasangan(id_barang, id_ruang)
			ORDER BY pasangan.id_barang, pasangan.id_ruang
			OFFSET 0
		) k`

	rows, err := db.QueryContext(ctx, query, barangIDs, ruangIDs)
	if err != nil {
		return fmt.Errorf("kunci saldo kartu_stok: %w", err)
	}
	defer rows.Close()

	// The locks are taken as the rows are produced, so the result set has to be drained
	// before the statement is done. Iterating and discarding is what forces that.
	for rows.Next() {
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("kunci saldo kartu_stok: %w", err)
	}

	return nil
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
