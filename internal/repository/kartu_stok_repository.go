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
//
// aktifIDUnitKerja scopes the rooms to the caller's active unit — isu #12 fase 6. Nil
// means unrestricted, the same reading it carries everywhere else. The filter sits on
// the outer query rather than inside the DISTINCT ON subquery: filtering a room out
// before it wins the DISTINCT ON could change which row wins for the rooms that remain
// (it cannot here, since DISTINCT ON is keyed per room, but keeping the filter outside
// is what makes that true by construction rather than by accident).
func (r *KartuStokRepository) SaldoPerRuang(ctx context.Context, db DBTX, idBarang int64, aktifIDUnitKerja *int64) ([]entity.SaldoStok, error) {
	const query = `
		SELECT id_barang, id_ruang, nama_ruang, stok_akhir, nilai_akhir,
			harga_pokok_satuan, tanggal_transaksi, created_at
		FROM (
			SELECT DISTINCT ON (ks.id_ruang) ` + saldoColumns + `, r.nama_ruang, r.id_unit_kerja
			FROM kartu_stok ks
			JOIN ruang r ON r.id = ks.id_ruang
			WHERE ks.id_barang = $1
			ORDER BY ks.id_ruang, ks.id DESC
		) saldo
		WHERE $2::BIGINT IS NULL OR id_unit_kerja = $2
		ORDER BY nama_ruang, id_ruang`

	rows, err := db.QueryContext(ctx, query, idBarang, aktifIDUnitKerja)
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

// SaldoRuang breaks one room's stock down by product — the mirror of
// SaldoPerRuang, which breaks one product down by room. stok_opname's TarikSaldo
// (isu #15) is the only caller: it needs every (product, this room) balance in one
// shot to seed the count sheet, and each row's own kartu_stok id becomes that
// line's id_kartu_stok_cutoff.
//
// Only products that have actually moved through the room appear — the same
// reading SaldoPerRuang takes, and the one the schema forces anyway:
// stok_opname_detail.id_kartu_stok_cutoff is NOT NULL, so a product with no
// kartu_stok row here has no reference to give it and cannot be pulled in at all.
// See "Barang yang sistem belum pernah lihat" in the usecase.
func (r *KartuStokRepository) SaldoRuang(ctx context.Context, db DBTX, idRuang int64) ([]entity.SaldoRuangBaris, error) {
	const query = `
		SELECT DISTINCT ON (ks.id_barang) ks.id_barang, ks.id, ks.stok_akhir
		FROM kartu_stok ks
		WHERE ks.id_ruang = $1
		ORDER BY ks.id_barang, ks.id DESC`

	rows, err := db.QueryContext(ctx, query, idRuang)
	if err != nil {
		return nil, fmt.Errorf("select saldo ruang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.SaldoRuangBaris, 0, 32)

	for rows.Next() {
		var baris entity.SaldoRuangBaris

		if err := rows.Scan(&baris.IDBarang, &baris.IDKartuStok, &baris.StokAkhir); err != nil {
			return nil, fmt.Errorf("scan saldo ruang: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saldo ruang: %w", err)
	}

	return list, nil
}

// HasSaldoPositif reports whether any product still has a positive balance in
// this room — isu #23 fase 3, the guard behind retiring a ruang.
//
// A retired room is not emptied by is_aktif = false: the inventory value report
// deliberately does not filter on is_aktif (see "Bacaan atas kartu_stok" —
// laporan/nilai-persediaan), because a retired room still holding stock still
// holds its value. Allowing the retirement anyway would leave goods that appear
// on no room list yet still sit on the balance sheet. The remedy is a mutasi or
// a pemakaian that actually empties the room first.
//
// Mirrors the same DISTINCT ON (id_barang) ... ORDER BY id DESC reading
// SaldoRuang takes: the latest kartu_stok row per product in this room is its
// current balance, and a product with no row here has never held stock in it at
// all.
func (r *KartuStokRepository) HasSaldoPositif(ctx context.Context, db DBTX, idRuang int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM (
				SELECT DISTINCT ON (id_barang) stok_akhir
				FROM kartu_stok
				WHERE id_ruang = $1
				ORDER BY id_barang, id DESC
			) saldo
			WHERE saldo.stok_akhir > 0
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, idRuang).Scan(&exists); err != nil {
		return false, fmt.Errorf("check saldo positif ruang: %w", err)
	}

	return exists, nil
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

// riwayatKartuStokFrom is shared by the COUNT and the row query in Riwayat, so
// total_item cannot start disagreeing with the rows it counts. ruang is joined here
// rather than only in the row query because the active-unit filter in
// riwayatKartuStokFilter reaches r.id_unit_kerja.
const riwayatKartuStokFrom = `
	FROM kartu_stok ks
	JOIN ruang r ON r.id = ks.id_ruang`

// riwayatKartuStokFilter is isu #22 fase 1's whole read: one product, one room —
// id_ruang is required, never optional, because a product's balance chain is
// partitioned by (id_barang, id_ruang) and mixing rooms would show a running balance
// that never actually existed on any single shelf.
//
// dari/sampai bound tanggal_transaksi, never created_at: the balance chain orders by
// id, and the date range only narrows which rows show, exactly the split CLAUDE.md
// calls out — id for order, date for range.
//
// $5 is the active-unit scope (isu #12 fase 6 applied to this new read). Unlike a
// Get, an id_ruang the caller does not have authority over does not 404 here: it
// simply matches nothing, the same silent-omission rule every list-shaped read in
// this issue follows, because there is no single resource identity to 404 against.
const riwayatKartuStokFilter = `
	WHERE ks.id_barang = $1
	  AND ks.id_ruang = $2
	  AND ($3::DATE IS NULL OR ks.tanggal_transaksi >= $3::DATE)
	  AND ($4::DATE IS NULL OR ks.tanggal_transaksi < ($4::DATE + INTERVAL '1 day'))
	  AND ($5::BIGINT IS NULL OR r.id_unit_kerja = $5)`

// riwayatKartuStokNomorDokumen translates ref_table + ref_id_transaksi into the
// document number a human reads, one CASE branch per writer kartu_stok has today.
// Every branch subquery is a primary-key lookup, so the cost is one index descent per
// row rather than the seven-way LEFT JOIN a naive translation would need — and unlike
// dokumen's polymorphic reference, nothing here interpolates a caller-supplied string
// into SQL: every ref_table this compares against is a literal already in this query.
//
// stok_opname is the one table in this list keyed on idstok_opname rather than id
// (CLAUDE.md: "the one table spelled that way").
const riwayatKartuStokNomorDokumen = `
		CASE ks.ref_table
			WHEN 'pembelian'          THEN (SELECT nomor FROM pembelian          WHERE id = ks.ref_id_transaksi)
			WHEN 'penerimaan_susulan' THEN (SELECT nomor FROM penerimaan_susulan WHERE id = ks.ref_id_transaksi)
			WHEN 'retur_pembelian'    THEN (SELECT nomor FROM retur_pembelian    WHERE id = ks.ref_id_transaksi)
			WHEN 'mutasi'             THEN (SELECT nomor FROM mutasi             WHERE id = ks.ref_id_transaksi)
			WHEN 'pemakaian'          THEN (SELECT nomor FROM pemakaian          WHERE id = ks.ref_id_transaksi)
			WHEN 'penjualan'          THEN (SELECT nomor FROM penjualan          WHERE id = ks.ref_id_transaksi)
			WHEN 'stok_opname'        THEN (SELECT nomor FROM stok_opname        WHERE idstok_opname = ks.ref_id_transaksi)
		END`

// riwayatKartuStokColumns names every projected column for the row query. si is
// LEFT JOINed rather than joined in riwayatKartuStokFrom: a reversing row can carry a
// NULL id_satuan_input, and the count query never needs the name at all.
const riwayatKartuStokColumns = `
	ks.id, ks.tanggal_transaksi, ks.jenis_transaksi,
	ks.stok_awal, ks.stok_masuk, ks.stok_keluar, ks.stok_akhir,
	ks.qty_input::TEXT, si.nama,
	ks.harga_pokok_satuan::TEXT, ks.nilai_masuk::TEXT, ks.nilai_keluar::TEXT, ks.nilai_akhir::TEXT,
	ks.ref_table, ks.ref_id_transaksi,` + riwayatKartuStokNomorDokumen + `,
	ks.id_kartu_stok_asal, ks.keterangan`

// Riwayat returns one product's movement history in one room, oldest first — isu #22
// fase 1, the first read of kartu_stok as a ledger rather than only a running balance.
//
// Ordered by id ascending, never by date: CLAUDE.md is explicit that the chain is
// built in id order and a reversal is dated after the fact, so date order can
// disagree with the order the trigger actually applied these rows in. A ledger is
// read from the top down, which is why fase 1 asks for ascending rather than the
// newest-first order every other list in this API uses.
func (r *KartuStokRepository) Riwayat(
	ctx context.Context, db DBTX, idBarang, idRuang int64, dari, sampai *string, aktifIDUnitKerja *int64, limit, offset int,
) ([]entity.RiwayatKartuStok, int64, error) {
	const countQuery = `SELECT COUNT(*)` + riwayatKartuStokFrom + riwayatKartuStokFilter

	var total int64
	if err := db.QueryRowContext(
		ctx, countQuery, idBarang, idRuang, dari, sampai, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count riwayat kartu_stok: %w", err)
	}

	if total == 0 {
		return []entity.RiwayatKartuStok{}, 0, nil
	}

	query := `SELECT ` + riwayatKartuStokColumns + riwayatKartuStokFrom + `
		LEFT JOIN satuan si ON si.id = ks.id_satuan_input` + riwayatKartuStokFilter + `
		ORDER BY ks.id
		LIMIT $6 OFFSET $7`

	rows, err := db.QueryContext(ctx, query, idBarang, idRuang, dari, sampai, aktifIDUnitKerja, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select riwayat kartu_stok: %w", err)
	}
	defer rows.Close()

	list := make([]entity.RiwayatKartuStok, 0, limit)

	for rows.Next() {
		var baris entity.RiwayatKartuStok

		if err := rows.Scan(
			&baris.ID, &baris.TanggalTransaksi, &baris.JenisTransaksi,
			&baris.StokAwal, &baris.StokMasuk, &baris.StokKeluar, &baris.StokAkhir,
			&baris.QtyInput, &baris.NamaSatuanInput,
			&baris.HargaPokokSatuan, &baris.NilaiMasuk, &baris.NilaiKeluar, &baris.NilaiAkhir,
			&baris.RefTable, &baris.RefIDTransaksi, &baris.NomorDokumen,
			&baris.IDKartuStokAsal, &baris.Keterangan,
		); err != nil {
			return nil, 0, fmt.Errorf("scan riwayat kartu_stok: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate riwayat kartu_stok: %w", err)
	}

	return list, total, nil
}

// SaldoPerRuangBatch reads the per-room breakdown behind many products at once — the
// batch counterpart to SaldoPerRuang, built for StokMinimum's result page so showing
// "which rooms" behind each flagged product costs one query rather than one per row
// on the page, the same reasoning SaldoBatch already applies to POS.
//
// idRuang narrows every product's breakdown to that one room, mirroring
// StokMinimum's own narrowing; nil means every room. aktifIDUnitKerja scopes rooms to
// the caller's active unit, nil meaning unrestricted — both readings this codebase
// uses everywhere else.
func (r *KartuStokRepository) SaldoPerRuangBatch(
	ctx context.Context, db DBTX, barangIDs []int64, idRuang, aktifIDUnitKerja *int64,
) (map[int64][]entity.SaldoStok, error) {
	const query = `
		SELECT id_barang, id_ruang, nama_ruang, stok_akhir, nilai_akhir,
			harga_pokok_satuan, tanggal_transaksi, created_at
		FROM (
			SELECT DISTINCT ON (ks.id_barang, ks.id_ruang) ` + saldoColumns + `, r.nama_ruang, r.id_unit_kerja
			FROM kartu_stok ks
			JOIN ruang r ON r.id = ks.id_ruang
			JOIN unnest($1::BIGINT[]) AS produk(id_barang) ON produk.id_barang = ks.id_barang
			ORDER BY ks.id_barang, ks.id_ruang, ks.id DESC
		) saldo
		WHERE ($2::BIGINT IS NULL OR id_ruang = $2)
		  AND ($3::BIGINT IS NULL OR id_unit_kerja = $3)
		ORDER BY id_barang, nama_ruang, id_ruang`

	rows, err := db.QueryContext(ctx, query, barangIDs, idRuang, aktifIDUnitKerja)
	if err != nil {
		return nil, fmt.Errorf("select saldo per ruang batch: %w", err)
	}
	defer rows.Close()

	saldo := make(map[int64][]entity.SaldoStok, len(barangIDs))

	for rows.Next() {
		var baris entity.SaldoStok
		var idBarang int64

		if err := rows.Scan(
			&idBarang, &baris.IDRuang, &baris.NamaRuang, &baris.StokAkhir,
			&baris.NilaiAkhir, &baris.HargaPokokSatuan,
			&baris.TanggalTransaksi, &baris.TerakhirDiperbarui,
		); err != nil {
			return nil, fmt.Errorf("scan saldo per ruang batch: %w", err)
		}

		baris.IDBarang = idBarang
		saldo[idBarang] = append(saldo[idBarang], baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saldo per ruang batch: %w", err)
	}

	return saldo, nil
}

// StokMinimum lists active products whose current stock has reached or fallen below
// their own stok_minimum — isu #22 fase 2.
//
// The CTE collapses kartu_stok to one row per (product, room) — the last one — before
// summing, the same DISTINCT ON shape SaldoPerRuang uses, because a naive
// SUM(stok_akhir) over every row in the chain would add up every movement ever made
// rather than the current balance.
//
// idRuang, given, narrows the sum to one room rather than every room the product has
// touched; aktifIDUnitKerja scopes it further to the caller's active unit_kerja. A
// product that has never moved any stock in the rooms left in scope gets no row from
// the LEFT JOIN and reads as a total of exactly 0 — below any positive minimum, and
// correctly so.
//
// stok_minimum = 0 is excluded in SQL, not filtered by the caller: 0 is the column's
// default and means "never set", and a report that could not tell the two apart would
// flag every never-configured product every single day.
//
// The threshold is total <= stok_minimum, not <: reaching the reorder point is the
// signal to reorder, and waiting for the strict inequality means always noticing one
// step late.
//
// Ordered by how far below minimum a product has fallen, worst first — a work queue,
// the same reasoning GET /supplier/{id}/utang orders oldest-first for.
func (r *KartuStokRepository) StokMinimum(
	ctx context.Context, db DBTX, idRuang, aktifIDUnitKerja *int64, limit, offset int,
) ([]entity.StokMinimumBaris, int64, error) {
	const cte = `
		WITH saldo_per_produk AS (
			SELECT s.id_barang, SUM(s.stok_akhir) AS total_stok
			FROM (
				SELECT DISTINCT ON (ks.id_barang, ks.id_ruang)
					ks.id_barang, ks.id_ruang, ks.stok_akhir, r.id_unit_kerja
				FROM kartu_stok ks
				JOIN ruang r ON r.id = ks.id_ruang
				ORDER BY ks.id_barang, ks.id_ruang, ks.id DESC
			) s
			WHERE ($1::BIGINT IS NULL OR s.id_ruang = $1)
			  AND ($2::BIGINT IS NULL OR s.id_unit_kerja = $2)
			GROUP BY s.id_barang
		)`

	const filter = `
		FROM product p
		LEFT JOIN saldo_per_produk sp ON sp.id_barang = p.id
		WHERE p.is_aktif
		  AND p.stok_minimum > 0
		  AND COALESCE(sp.total_stok, 0) <= p.stok_minimum`

	var total int64
	if err := db.QueryRowContext(
		ctx, cte+`SELECT COUNT(*)`+filter, idRuang, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count stok minimum: %w", err)
	}

	if total == 0 {
		return []entity.StokMinimumBaris{}, 0, nil
	}

	query := cte + `
		SELECT p.id, p.kode_barang, p.nama, p.stok_minimum, COALESCE(sp.total_stok, 0)` + filter + `
		ORDER BY (p.stok_minimum - COALESCE(sp.total_stok, 0)) DESC, p.nama, p.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, idRuang, aktifIDUnitKerja, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select stok minimum: %w", err)
	}
	defer rows.Close()

	list := make([]entity.StokMinimumBaris, 0, limit)

	for rows.Next() {
		var baris entity.StokMinimumBaris

		if err := rows.Scan(
			&baris.IDProduct, &baris.KodeBarang, &baris.NamaProduct,
			&baris.StokMinimum, &baris.TotalStok,
		); err != nil {
			return nil, 0, fmt.Errorf("scan stok minimum: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate stok minimum: %w", err)
	}

	return list, total, nil
}

// NilaiPersediaan sums nilai_akhir off the last row of every (product, room) chain,
// grouped by room — isu #22 fase 3's first report. GET /product/{id}/stok
// (SaldoPerRuang) already reads exactly this shape for one product; this is the
// cross-room, cross-product recap sitting on top of it.
//
// idRuang narrows to one room; aktifIDUnitKerja scopes to the caller's active
// unit_kerja. ruang.is_aktif is never filtered — CLAUDE.md is explicit that a retired
// room still holding stock still holds its value, and a report hiding that would
// misstate the balance sheet it exists to state. A room with no movements at all
// still appears, at a total of 0: it is a fact about that room, not a missing one.
func (r *KartuStokRepository) NilaiPersediaan(
	ctx context.Context, db DBTX, idRuang, aktifIDUnitKerja *int64,
) ([]entity.NilaiPersediaanBaris, error) {
	const query = `
		SELECT r.id, r.nama_ruang, COALESCE(SUM(saldo.nilai_akhir), 0)::TEXT
		FROM ruang r
		LEFT JOIN (
			SELECT DISTINCT ON (ks.id_barang, ks.id_ruang) ks.id_ruang, ks.nilai_akhir
			FROM kartu_stok ks
			ORDER BY ks.id_barang, ks.id_ruang, ks.id DESC
		) saldo ON saldo.id_ruang = r.id
		WHERE ($1::BIGINT IS NULL OR r.id = $1)
		  AND ($2::BIGINT IS NULL OR r.id_unit_kerja = $2)
		GROUP BY r.id, r.nama_ruang
		ORDER BY r.nama_ruang, r.id`

	rows, err := db.QueryContext(ctx, query, idRuang, aktifIDUnitKerja)
	if err != nil {
		return nil, fmt.Errorf("select nilai persediaan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.NilaiPersediaanBaris, 0, 8)

	for rows.Next() {
		var baris entity.NilaiPersediaanBaris

		if err := rows.Scan(&baris.IDRuang, &baris.NamaRuang, &baris.TotalNilai); err != nil {
			return nil, fmt.Errorf("scan nilai persediaan: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nilai persediaan: %w", err)
	}

	return list, nil
}

// Pergerakan sums stok_masuk and stok_keluar per (product, room, jenis_transaksi)
// inside a date range — isu #22 fase 3's third report, and the one that makes a
// stok_opname's shrinkage readable as a monthly figure instead of one document at a
// time.
//
// The range filters tanggal_transaksi, which is what makes the deliberate trap in
// this issue visible rather than hidden: a document posted in one period and
// cancelled in a later one produces a reversing row dated when the cancellation
// happened (isu #6), so it surfaces in the range covering the cancellation, never the
// range covering the original posting. Summing by created_at or by the parent
// document's own tanggal would silently put it back in the wrong period.
func (r *KartuStokRepository) Pergerakan(
	ctx context.Context, db DBTX, dari, sampai *string, idRuang, idProduct, aktifIDUnitKerja *int64,
) ([]entity.PergerakanBaris, error) {
	const query = `
		SELECT ks.id_barang, p.kode_barang, p.nama, ks.id_ruang, r.nama_ruang, ks.jenis_transaksi,
			SUM(ks.stok_masuk), SUM(ks.stok_keluar)
		FROM kartu_stok ks
		JOIN product p ON p.id = ks.id_barang
		JOIN ruang r ON r.id = ks.id_ruang
		WHERE ($1::DATE IS NULL OR ks.tanggal_transaksi >= $1::DATE)
		  AND ($2::DATE IS NULL OR ks.tanggal_transaksi < ($2::DATE + INTERVAL '1 day'))
		  AND ($3::BIGINT IS NULL OR ks.id_ruang = $3)
		  AND ($4::BIGINT IS NULL OR ks.id_barang = $4)
		  AND ($5::BIGINT IS NULL OR r.id_unit_kerja = $5)
		GROUP BY ks.id_barang, p.kode_barang, p.nama, ks.id_ruang, r.nama_ruang, ks.jenis_transaksi
		ORDER BY p.nama, ks.id_barang, r.nama_ruang, ks.id_ruang, ks.jenis_transaksi`

	rows, err := db.QueryContext(ctx, query, dari, sampai, idRuang, idProduct, aktifIDUnitKerja)
	if err != nil {
		return nil, fmt.Errorf("select pergerakan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PergerakanBaris, 0, 16)

	for rows.Next() {
		var baris entity.PergerakanBaris

		if err := rows.Scan(
			&baris.IDProduct, &baris.KodeBarang, &baris.NamaProduct,
			&baris.IDRuang, &baris.NamaRuang, &baris.JenisTransaksi,
			&baris.TotalMasuk, &baris.TotalKeluar,
		); err != nil {
			return nil, fmt.Errorf("scan pergerakan: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pergerakan: %w", err)
	}

	return list, nil
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
