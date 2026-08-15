package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PemakaianRepository owns every statement touching pemakaian and pemakaian_detail.
type PemakaianRepository struct{}

func NewPemakaianRepository() *PemakaianRepository {
	return &PemakaianRepository{}
}

// pemakaianColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
//
// total_hpp is cast to TEXT: it is NUMERIC(20,2) and nullable, and scanning NUMERIC
// into a float64 would round the one figure that answers what this month's internal
// usage actually consumed.
const pemakaianColumns = `id, nomor, tanggal, id_ruang, id_pemohon, keperluan, status,
	disetujui_oleh, ts_disetujui, catatan_persetujuan, total_hpp::TEXT,
	created_by, created_at, posted_at, dibatalkan_oleh, alasan_batal`

// pemakaianReadColumns adds the room's name and the requester's display name.
// Fetching either per row would be an N+1.
//
// pemohon.nama_lengkap is nullable (migration 000002 declares it VARCHAR with no
// NOT NULL) even though id_pemohon itself is required, so the join falls back to
// username rather than risk scanning NULL into a plain string.
const pemakaianReadColumns = `pm.id, pm.nomor, pm.tanggal, pm.id_ruang, pm.id_pemohon,
	pm.keperluan, pm.status, pm.disetujui_oleh, pm.ts_disetujui, pm.catatan_persetujuan,
	pm.total_hpp::TEXT, pm.created_by, pm.created_at, pm.posted_at,
	pm.dibatalkan_oleh, pm.alasan_batal,
	r.nama_ruang, COALESCE(pemohon.nama_lengkap, pemohon.username)`

// pemakaianFrom joins INNER on both: id_ruang and id_pemohon are NOT NULL, so a
// pemakaian without a room or a requester cannot exist.
const pemakaianFrom = `
	FROM pemakaian pm
	JOIN ruang r ON r.id = pm.id_ruang
	JOIN users pemohon ON pemohon.id = pm.id_pemohon`

// pemakaianFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$6 and pagination follows after it.
const pemakaianFilter = `
	WHERE ($1 = '' OR pm.nomor ILIKE '%' || $1 || '%' OR pm.keperluan ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR pm.status = $2)
	  AND ($3 = 0 OR pm.id_ruang = $3)
	  AND ($4 = 0 OR pm.id_pemohon = $4)
	  AND ($5::DATE IS NULL OR pm.tanggal >= $5::DATE)
	  AND ($6::DATE IS NULL OR pm.tanggal < ($6::DATE + INTERVAL '1 day'))`

const pemakaianDetailReadColumns = `pd.id, pd.id_pemakaian, pd.id_product, pd.qty_input::TEXT,
	pd.id_satuan_input, pd.faktor_konversi, pd.qty_dasar, pd.qty_disetujui_dasar,
	pd.hpp_satuan_dasar::TEXT, pd.hpp_total::TEXT, pd.keterangan,
	pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const pemakaianDetailFrom = `
	FROM pemakaian_detail pd
	JOIN product pr ON pr.id = pd.id_product
	JOIN satuan si ON si.id = pd.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// PemakaianPatch carries a partial header update. All four fields are NOT NULL
// columns, so the usecase rejects an explicit null before this ever runs — this is
// COALESCE, not the CASE WHEN dance a nullable column needs.
type PemakaianPatch struct {
	Tanggal   *time.Time
	IDRuang   *int64
	IDPemohon *int64
	Keperluan *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can
// reach the joined names.
func (r *PemakaianRepository) Create(ctx context.Context, db DBTX, pemakaian *entity.Pemakaian) error {
	const query = `
		INSERT INTO pemakaian (
			nomor, tanggal, id_ruang, id_pemohon, keperluan, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		pemakaian.Nomor, pemakaian.Tanggal, pemakaian.IDRuang, pemakaian.IDPemohon,
		pemakaian.Keperluan, pemakaian.Status, pemakaian.CreatedBy,
	).Scan(&pemakaian.ID, &pemakaian.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pemakaian: %w", err)
	}

	return nil
}

func (r *PemakaianRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Pemakaian, error) {
	const query = `SELECT ` + pemakaianReadColumns + pemakaianFrom + ` WHERE pm.id = $1`

	pemakaian := new(entity.Pemakaian)

	if err := scanPemakaianRead(db.QueryRowContext(ctx, query, id), pemakaian); err != nil {
		return nil, err
	}

	return pemakaian, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *PemakaianRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.Pemakaian, error) {
	const query = `SELECT ` + pemakaianColumns + ` FROM pemakaian WHERE id = $1 FOR UPDATE`

	pemakaian := new(entity.Pemakaian)

	if err := scanPemakaian(db.QueryRowContext(ctx, query, id), pemakaian); err != nil {
		return nil, err
	}

	return pemakaian, nil
}

// UpdateHeader patches a DRAFT. The caller is expected to have taken the row lock
// and checked the status already (kunciDenganStatus), so this carries no status
// guard of its own — same shape as MutasiRepository.UpdateHeader.
func (r *PemakaianRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch PemakaianPatch) error {
	const query = `
		UPDATE pemakaian SET
			tanggal    = COALESCE($2, tanggal),
			id_ruang   = COALESCE($3, id_ruang),
			id_pemohon = COALESCE($4, id_pemohon),
			keperluan  = COALESCE($5, keperluan)
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id, patch.Tanggal, patch.IDRuang, patch.IDPemohon, patch.Keperluan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// Ajukan hands a draft to the approver.
func (r *PemakaianRepository) Ajukan(ctx context.Context, db DBTX, id int64) error {
	const query = `UPDATE pemakaian SET status = 'DIAJUKAN' WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan pemakaian", id)
}

// Setujui approves a submission. disetujui_oleh and ts_disetujui are the only actor
// and timestamp columns a decision has, so Tolak below reuses both for a rejection
// too — the schema carries no separate ditolak_oleh.
func (r *PemakaianRepository) Setujui(ctx context.Context, db DBTX, id, actorID int64, catatan *string) error {
	const query = `
		UPDATE pemakaian SET
			status               = 'DISETUJUI',
			disetujui_oleh       = $2,
			ts_disetujui         = now(),
			catatan_persetujuan  = $3
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "setujui pemakaian", id, actorID, catatan)
}

// Tolak refuses a submission outright. Unlike pembelian this does not return the
// document to DRAFT — see entity.StatusPemakaianDitolak — so it is a terminal write,
// not a resubmission cycle. The reason lands in catatan_persetujuan, the same column
// an approval note would use.
func (r *PemakaianRepository) Tolak(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE pemakaian SET
			status              = 'DITOLAK',
			disetujui_oleh      = $2,
			ts_disetujui        = now(),
			catatan_persetujuan = $3
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak pemakaian", id, actorID, alasan)
}

// Posting closes the document. Called after every kartu_stok row is written, in the
// same transaction, so a failure at either end leaves neither.
//
// No actor column: who was allowed to call this is the route guard's answer, and
// disetujui_oleh already records who allowed the goods to leave.
func (r *PemakaianRepository) Posting(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE pemakaian SET
			posted_at = now(),
			status    = 'POSTED'
		WHERE id = $1 AND status = 'DISETUJUI'`

	return execTransisi(ctx, db, query, "posting pemakaian", id)
}

// Batal marks a posted document void. The reversing kartu_stok rows are written by
// the usecase in the same transaction; nothing is deleted anywhere.
func (r *PemakaianRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE pemakaian SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal pemakaian", id, actorID, alasan)
}

// RecalculateTotalHPP writes the header's total, summed by the usecase from every
// line's hpp_total after posting. Not a cache recomputed on every read: it exists
// only once, right after the loop that writes kartu_stok, and nothing about a POSTED
// document's lines can change afterwards.
func (r *PemakaianRepository) RecalculateTotalHPP(ctx context.Context, db DBTX, id int64, totalHPP string) error {
	const query = `UPDATE pemakaian SET total_hpp = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, totalHPP); err != nil {
		return fmt.Errorf("update pemakaian total_hpp: %w", err)
	}

	return nil
}

// Search returns one page plus the total matching count.
//
// terlamaDulu flips the ordering to oldest first — status=DIAJUKAN with this set is
// the approval queue, read to be worked through rather than to see the newest, the
// same choice GET /supplier/{id}/utang makes for its own queue.
func (r *PemakaianRepository) Search(ctx context.Context, db DBTX, search, status string, idRuang, idPemohon int64, dari, sampai *string, terlamaDulu bool, limit, offset int) ([]entity.Pemakaian, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+pemakaianFrom+pemakaianFilter,
		search, status, idRuang, idPemohon, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count pemakaian: %w", err)
	}

	if total == 0 {
		return []entity.Pemakaian{}, 0, nil
	}

	// Either way the ORDER BY ends in a unique column, so a page boundary between
	// same-day documents cannot repeat or skip one. pemakaian_tanggal_id_idx from
	// migration 000021 supports the descending form directly and the ascending one
	// by reverse scan.
	urutan := `ORDER BY pm.tanggal DESC, pm.id DESC`
	if terlamaDulu {
		urutan = `ORDER BY pm.tanggal ASC, pm.id ASC`
	}

	query := `SELECT ` + pemakaianReadColumns + pemakaianFrom + pemakaianFilter + `
		` + urutan + `
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(ctx, query, search, status, idRuang, idPemohon, dari, sampai, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select pemakaian: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Pemakaian, 0, limit)

	for rows.Next() {
		var pemakaian entity.Pemakaian

		if err := scanPemakaianRead(rows, &pemakaian); err != nil {
			return nil, 0, fmt.Errorf("scan pemakaian: %w", err)
		}

		list = append(list, pemakaian)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pemakaian: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
//
// qty_disetujui_dasar, hpp_satuan_dasar, and hpp_total are deliberately absent from
// the column list: a DRAFT has no answer for any of them. The first is filled by
// UpdateDetailQtyDisetujui at Setujui, the other two by UpdateDetailPosting at
// Posting, from what the outgoing kartu_stok row's RETURNING reported.
//
// No unique index on (id_pemakaian, id_product) — migration 000021 says why: the
// quota here is the room's balance, checked in total at posting, not a quantity held
// on a parent line. The same product may appear on more than one line.
func (r *PemakaianRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.PemakaianDetail) error {
	const query = `
		INSERT INTO pemakaian_detail (
			id_pemakaian, id_product, qty_input, id_satuan_input, faktor_konversi,
			qty_dasar, keterangan
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDPemakaian, detail.IDProduct, detail.QtyInput, detail.IDSatuanInput,
		detail.FaktorKonversi, detail.QtyDasar, detail.Keterangan,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert pemakaian_detail: %w", err)
	}

	return nil
}

// UpdateDetailQtyDisetujui records how much of one line the approver actually
// allowed to leave. Written at Setujui, never at Create — filling it earlier would
// let a requester approve their own request through the back door.
func (r *PemakaianRepository) UpdateDetailQtyDisetujui(ctx context.Context, db DBTX, id, qtyDisetujuiDasar int64) error {
	const query = `UPDATE pemakaian_detail SET qty_disetujui_dasar = $2 WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, qtyDisetujuiDasar); err != nil {
		return fmt.Errorf("update pemakaian_detail qty_disetujui_dasar: %w", err)
	}

	return nil
}

// UpdateDetailPosting records what the room's stock actually cost, copied from the
// harga_pokok_satuan and nilai_keluar the outgoing kartu_stok row reported back — the
// application cannot know the moving average before the insert. See
// entity.PemakaianDetail.HPPSatuanDasar.
func (r *PemakaianRepository) UpdateDetailPosting(ctx context.Context, db DBTX, id int64, hppSatuanDasar, hppTotal string) error {
	const query = `
		UPDATE pemakaian_detail SET
			hpp_satuan_dasar = $2::NUMERIC,
			hpp_total        = $3::NUMERIC
		WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, hppSatuanDasar, hppTotal); err != nil {
		return fmt.Errorf("update pemakaian_detail posting: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /pemakaian/{id}/detail performs. Only ever reached on a DRAFT.
func (r *PemakaianRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM pemakaian_detail WHERE id_pemakaian = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete pemakaian_detail: %w", err)
	}

	return nil
}

func (r *PemakaianRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.PemakaianDetail, error) {
	const query = `SELECT ` + pemakaianDetailReadColumns + pemakaianDetailFrom + `
		WHERE pd.id_pemakaian = $1
		ORDER BY pd.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select pemakaian_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PemakaianDetail, 0, 8)

	for rows.Next() {
		var detail entity.PemakaianDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDPemakaian, &detail.IDProduct, &detail.QtyInput,
			&detail.IDSatuanInput, &detail.FaktorKonversi, &detail.QtyDasar,
			&detail.QtyDisetujuiDasar, &detail.HPPSatuanDasar, &detail.HPPTotal,
			&detail.Keterangan,
			&detail.KodeBarang, &detail.NamaProduct, &detail.NamaSatuan,
			&detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan pemakaian_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pemakaian_detail: %w", err)
	}

	return list, nil
}

// pemakaianFields lists the scan targets in the order of pemakaianColumns, once, so
// the two read paths cannot drift apart from each other or from the constant.
func pemakaianFields(pemakaian *entity.Pemakaian) []any {
	return []any{
		&pemakaian.ID, &pemakaian.Nomor, &pemakaian.Tanggal,
		&pemakaian.IDRuang, &pemakaian.IDPemohon, &pemakaian.Keperluan, &pemakaian.Status,
		&pemakaian.DisetujuiOleh, &pemakaian.TsDisetujui, &pemakaian.CatatanPersetujuan,
		&pemakaian.TotalHPP,
		&pemakaian.CreatedBy, &pemakaian.CreatedAt, &pemakaian.PostedAt,
		&pemakaian.DibatalkanOleh, &pemakaian.AlasanBatal,
	}
}

func scanPemakaian(row rowScanner, pemakaian *entity.Pemakaian) error {
	return row.Scan(pemakaianFields(pemakaian)...)
}

func scanPemakaianRead(row rowScanner, pemakaian *entity.Pemakaian) error {
	return row.Scan(append(
		pemakaianFields(pemakaian), &pemakaian.NamaRuang, &pemakaian.NamaPemohon,
	)...)
}
