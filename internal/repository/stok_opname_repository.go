package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// StokOpnameRepository owns every statement touching stok_opname and
// stok_opname_detail.
//
// The primary key columns are idstok_opname and idstok_opname_detail, not id —
// the one table in this project spelled that way. Every statement below names
// them explicitly rather than "fixing" them through a rename migration: they
// already carry foreign keys pointing at them and nothing is gained by the
// churn.
type StokOpnameRepository struct{}

func NewStokOpnameRepository() *StokOpnameRepository {
	return &StokOpnameRepository{}
}

// stokOpnameColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined ruang.
const stokOpnameColumns = `idstok_opname, nomor, id_ruang, tgl_buka, tgl_tutup, ts_cutoff,
	uraian_so, status, created_by, created_at, verified_by, ts_verified, posted_at,
	dibatalkan_oleh, alasan_batal, ts_batal`

// stokOpnameReadColumns adds the room's name and its unit_kerja (isu #12 fase 6).
// Fetching either per row would be an N+1.
const stokOpnameReadColumns = `so.idstok_opname, so.nomor, so.id_ruang, so.tgl_buka,
	so.tgl_tutup, so.ts_cutoff, so.uraian_so, so.status, so.created_by, so.created_at,
	so.verified_by, so.ts_verified, so.posted_at,
	so.dibatalkan_oleh, so.alasan_batal, so.ts_batal,
	r.nama_ruang, r.id_unit_kerja`

// stokOpnameFrom joins ruang INNER: id_ruang is NOT NULL, so a stok_opname without
// a room cannot exist.
const stokOpnameFrom = `
	FROM stok_opname so
	JOIN ruang r ON r.id = so.id_ruang`

// stokOpnameFilter is shared by the COUNT and the row query. Two copies of a
// filter eventually diverge and total_item starts lying about the data.
//
// $6 is the active-unit scope (isu #12 fase 6), against r.id_unit_kerja — reaching
// it is why the COUNT query has to use stokOpnameFrom too, instead of a bare
// FROM stok_opname so.
const stokOpnameFilter = `
	WHERE ($1 = '' OR so.nomor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR so.status = $2)
	  AND ($3 = 0 OR so.id_ruang = $3)
	  AND ($4::DATE IS NULL OR so.tgl_buka >= $4::DATE)
	  AND ($5::DATE IS NULL OR so.tgl_buka < ($5::DATE + INTERVAL '1 day'))
	  AND ($6::BIGINT IS NULL OR r.id_unit_kerja = $6)`

const stokOpnameDetailReadColumns = `sod.idstok_opname_detail, sod.id_stok_opname,
	sod.id_barang, sod.id_ruang, sod.stok_awal, sod.stok_so,
	sod.stok_selisih_lebih, sod.stok_selisih_kurang, sod.keterangan,
	sod.id_kartu_stok_cutoff, sod.id_kartu_stok_penyesuaian, sod.updated_by, sod.ts_update,
	pr.kode_barang, pr.nama, sdasar.nama`

const stokOpnameDetailFrom = `
	FROM stok_opname_detail sod
	JOIN product pr ON pr.id = sod.id_barang
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// Create inserts the header and fills ID. TsCutoff must already be set by the
// usecase from now(), never from a request body — a client-chosen cutoff is a
// selisih the client gets to pick.
func (r *StokOpnameRepository) Create(ctx context.Context, db DBTX, opname *entity.StokOpname) error {
	const query = `
		INSERT INTO stok_opname (
			nomor, id_ruang, tgl_buka, ts_cutoff, uraian_so, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING idstok_opname, created_at`

	err := db.QueryRowContext(
		ctx, query,
		opname.Nomor, opname.IDRuang, opname.TglBuka, opname.TsCutoff, opname.UraianSO,
		opname.Status, opname.CreatedBy,
	).Scan(&opname.ID, &opname.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert stok_opname: %w", err)
	}

	return nil
}

func (r *StokOpnameRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.StokOpname, error) {
	const query = `SELECT ` + stokOpnameReadColumns + stokOpnameFrom + ` WHERE so.idstok_opname = $1`

	opname := new(entity.StokOpname)

	if err := scanStokOpnameRead(db.QueryRowContext(ctx, query, id), opname); err != nil {
		return nil, err
	}

	return opname, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *StokOpnameRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.StokOpname, error) {
	const query = `SELECT ` + stokOpnameColumns + ` FROM stok_opname WHERE idstok_opname = $1 FOR UPDATE`

	opname := new(entity.StokOpname)

	if err := scanStokOpname(db.QueryRowContext(ctx, query, id), opname); err != nil {
		return nil, err
	}

	return opname, nil
}

// FindOpenByRuang returns the open (DRAFT/DIAJUKAN) opname freezing a room, or
// sql.ErrNoRows when the room is free. stok_opname_ruang_terbuka_uidx guarantees
// at most one such row exists.
//
// This is periksaRuangBeku's only caller — a read for the message, never the
// guard. The kartu_stok trigger decides under the ruang: advisory lock, and this
// can be stale the instant after it is read; that is fine, because nothing here
// is trusted to be the last word.
func (r *StokOpnameRepository) FindOpenByRuang(ctx context.Context, db DBTX, idRuang int64) (*entity.StokOpname, error) {
	const query = `
		SELECT so.idstok_opname, so.nomor, r.nama_ruang
		FROM stok_opname so
		JOIN ruang r ON r.id = so.id_ruang
		WHERE so.id_ruang = $1 AND so.status IN ('DRAFT', 'DIAJUKAN')`

	opname := new(entity.StokOpname)

	err := db.QueryRowContext(ctx, query, idRuang).Scan(&opname.ID, &opname.Nomor, &opname.NamaRuang)
	if err != nil {
		return nil, err
	}

	return opname, nil
}

// UpdateUraian patches uraian_so on a DRAFT. It is the only header field a PATCH
// may touch: everything else that could change what the document means is
// resolved by a different endpoint or not at all.
func (r *StokOpnameRepository) UpdateUraian(ctx context.Context, db DBTX, id int64, setUraian bool, uraian *string) error {
	const query = `
		UPDATE stok_opname SET
			uraian_so = CASE WHEN $2::BOOLEAN THEN $3 ELSE uraian_so END
		WHERE idstok_opname = $1
		RETURNING idstok_opname`

	var updated int64

	if err := db.QueryRowContext(ctx, query, id, setUraian, uraian).Scan(&updated); err != nil {
		return err
	}

	return nil
}

// Ajukan hands a draft count to the verifier and stamps tgl_tutup — the date the
// count itself finished, distinct from posted_at, which answers when the books
// were settled.
func (r *StokOpnameRepository) Ajukan(ctx context.Context, db DBTX, id int64, tglTutup time.Time) error {
	const query = `
		UPDATE stok_opname SET
			status    = 'DIAJUKAN',
			tgl_tutup = $2
		WHERE idstok_opname = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan stok_opname", id, tglTutup)
}

// Tolak sends a submission back to DRAFT for a recount. Unlike pemakaian's
// Tolak this is not terminal: a rejected count still has to be reconciled, and
// the room stays frozen either way, so looping back is the correct shape rather
// than a business refusal.
//
// verified_by/ts_verified are reused for a rejection too — the schema carries no
// separate column for "who sent this back", the same reuse
// entity.Pemakaian.DisetujuiOleh makes of one pair of columns for both outcomes.
func (r *StokOpnameRepository) Tolak(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE stok_opname SET
			status      = 'DRAFT',
			verified_by = $2,
			ts_verified = now()
		WHERE idstok_opname = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak stok_opname", id, actorID)
}

// Posting closes the document. Called after every kartu_stok adjustment row is
// written, in the same transaction, so a failure at either end leaves neither.
func (r *StokOpnameRepository) Posting(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE stok_opname SET
			status      = 'POSTED',
			verified_by = $2,
			ts_verified = now(),
			posted_at   = now()
		WHERE idstok_opname = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "posting stok_opname", id, actorID)
}

// Batal voids a document from any non-BATAL status. From DRAFT/DIAJUKAN nothing
// has touched kartu_stok yet, so this only changes the status and — through the
// usecase's ruang: lock — releases the freeze. From POSTED the usecase has
// already appended reversing kartu_stok rows before this runs.
func (r *StokOpnameRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE stok_opname SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3,
			ts_batal        = now()
		WHERE idstok_opname = $1 AND status <> 'BATAL'`

	return execTransisi(ctx, db, query, "batal stok_opname", id, actorID, alasan)
}

// Search returns one page plus the total matching count.
//
// terlamaDulu flips the ordering to oldest first — status=DIAJUKAN with this set
// is the verification queue, the same shape GET /supplier/{id}/utang uses for its
// own.
func (r *StokOpnameRepository) Search(ctx context.Context, db DBTX, search, status string, idRuang int64, dari, sampai *string, aktifIDUnitKerja *int64, terlamaDulu bool, limit, offset int) ([]entity.StokOpname, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+stokOpnameFrom+stokOpnameFilter,
		search, status, idRuang, dari, sampai, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count stok_opname: %w", err)
	}

	if total == 0 {
		return []entity.StokOpname{}, 0, nil
	}

	urutan := `ORDER BY so.tgl_buka DESC, so.idstok_opname DESC`
	if terlamaDulu {
		urutan = `ORDER BY so.tgl_buka ASC, so.idstok_opname ASC`
	}

	query := `SELECT ` + stokOpnameReadColumns + stokOpnameFrom + stokOpnameFilter + `
		` + urutan + `
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(ctx, query, search, status, idRuang, dari, sampai, aktifIDUnitKerja, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select stok_opname: %w", err)
	}
	defer rows.Close()

	list := make([]entity.StokOpname, 0, limit)

	for rows.Next() {
		var opname entity.StokOpname

		if err := scanStokOpnameRead(rows, &opname); err != nil {
			return nil, 0, fmt.Errorf("scan stok_opname: %w", err)
		}

		list = append(list, opname)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate stok_opname: %w", err)
	}

	return list, total, nil
}

// HasDetail reports whether any line has been written yet. TarikSaldo refuses to
// run a second time when this is true: pulling the balance twice into one
// document is the cleanest way to get two snapshots inside what is supposed to be
// one.
func (r *StokOpnameRepository) HasDetail(ctx context.Context, db DBTX, id int64) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM stok_opname_detail WHERE id_stok_opname = $1)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check stok_opname_detail: %w", err)
	}

	return exists, nil
}

// InsertDetail writes one line — used both by TarikSaldo's bulk seed and by the
// wholesale PUT .../detail replace, which is also how a line the automatic pull
// missed gets added by hand. stok_so is deliberately absent from the column list:
// a freshly pulled or added line has not been counted yet, and NULL is what says
// so.
//
// stok_opname_detail_unik_uidx stops one (product, room) pair appearing twice in
// one document; the usecase maps that to a message naming the product.
func (r *StokOpnameRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.StokOpnameDetail) error {
	const query = `
		INSERT INTO stok_opname_detail (
			id_stok_opname, id_barang, id_ruang, stok_awal, id_kartu_stok_cutoff
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING idstok_opname_detail`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDStokOpname, detail.IDBarang, detail.IDRuang, detail.StokAwal,
		detail.IDKartuStokCutoff,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert stok_opname_detail: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /stok_opname/{id}/detail performs. Only ever reached on a DRAFT.
func (r *StokOpnameRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM stok_opname_detail WHERE id_stok_opname = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete stok_opname_detail: %w", err)
	}

	return nil
}

func (r *StokOpnameRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.StokOpnameDetail, error) {
	const query = `SELECT ` + stokOpnameDetailReadColumns + stokOpnameDetailFrom + `
		WHERE sod.id_stok_opname = $1
		ORDER BY sod.idstok_opname_detail`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select stok_opname_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.StokOpnameDetail, 0, 32)

	for rows.Next() {
		var detail entity.StokOpnameDetail

		if err := scanStokOpnameDetail(rows, &detail); err != nil {
			return nil, fmt.Errorf("scan stok_opname_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stok_opname_detail: %w", err)
	}

	return list, nil
}

// CountBelumDihitung answers two questions in one query: how many lines this
// document has, and how many of them still have stok_so = NULL. The usecase uses
// the pair to refuse Ajukan on an empty count and to report how much of a partial
// count is still outstanding.
func (r *StokOpnameRepository) CountBelumDihitung(ctx context.Context, db DBTX, id int64) (total, belumDihitung int, err error) {
	const query = `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE stok_so IS NULL)
		FROM stok_opname_detail
		WHERE id_stok_opname = $1`

	if err := db.QueryRowContext(ctx, query, id).Scan(&total, &belumDihitung); err != nil {
		return 0, 0, fmt.Errorf("count stok_opname_detail: %w", err)
	}

	return total, belumDihitung, nil
}

// UpdateDetailHitung fills in one line's physical count and/or its note, and
// always recomputes stok_selisih_lebih/stok_selisih_kurang from the result —
// never from a form, the same rule status_pembayaran follows. The CASE
// expressions read the row's current stok_so when this call is not the one
// setting it, so a keterangan-only patch recomputes the same selisih rather than
// zeroing it.
//
// A stok_so left NULL (setStokSO false, or an explicit null the usecase already
// rejects — see UpdateStokOpnameDetailRequest) folds selisih to zero through
// COALESCE(..., stok_awal): stok_awal - stok_awal is 0, which is the correct
// reading for "not counted yet", not a false deficit.
func (r *StokOpnameRepository) UpdateDetailHitung(ctx context.Context, db DBTX, idStokOpname, idDetail, actorID int64, setStokSO bool, stokSO *int64, setKeterangan bool, keterangan *string) error {
	const query = `
		UPDATE stok_opname_detail SET
			stok_so = CASE WHEN $4::BOOLEAN THEN $5 ELSE stok_so END,
			stok_selisih_lebih = GREATEST(
				COALESCE(CASE WHEN $4::BOOLEAN THEN $5 ELSE stok_so END, stok_awal) - stok_awal, 0
			),
			stok_selisih_kurang = GREATEST(
				stok_awal - COALESCE(CASE WHEN $4::BOOLEAN THEN $5 ELSE stok_so END, stok_awal), 0
			),
			keterangan = CASE WHEN $6::BOOLEAN THEN $7 ELSE keterangan END,
			updated_by = $3,
			ts_update  = now()
		WHERE id_stok_opname = $1 AND idstok_opname_detail = $2
		RETURNING idstok_opname_detail`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, idStokOpname, idDetail, actorID, setStokSO, stokSO, setKeterangan, keterangan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// UpdateDetailPosting records which adjustment row a line produced. Left NULL
// forever on a line whose selisih came out zero — nothing was posted for it, so
// nothing to point at.
func (r *StokOpnameRepository) UpdateDetailPosting(ctx context.Context, db DBTX, id, idKartuStokPenyesuaian int64) error {
	const query = `UPDATE stok_opname_detail SET id_kartu_stok_penyesuaian = $2 WHERE idstok_opname_detail = $1`

	if _, err := db.ExecContext(ctx, query, id, idKartuStokPenyesuaian); err != nil {
		return fmt.Errorf("update stok_opname_detail posting: %w", err)
	}

	return nil
}

// stokOpnameFields lists the scan targets in the order of stokOpnameColumns,
// once, so the two read paths cannot drift apart from each other or from the
// constant.
func stokOpnameFields(opname *entity.StokOpname) []any {
	return []any{
		&opname.ID, &opname.Nomor, &opname.IDRuang, &opname.TglBuka, &opname.TglTutup,
		&opname.TsCutoff, &opname.UraianSO, &opname.Status, &opname.CreatedBy, &opname.CreatedAt,
		&opname.VerifiedBy, &opname.TsVerified, &opname.PostedAt,
		&opname.DibatalkanOleh, &opname.AlasanBatal, &opname.TsBatal,
	}
}

func scanStokOpname(row rowScanner, opname *entity.StokOpname) error {
	return row.Scan(stokOpnameFields(opname)...)
}

func scanStokOpnameRead(row rowScanner, opname *entity.StokOpname) error {
	return row.Scan(append(
		stokOpnameFields(opname), &opname.NamaRuang, &opname.IDUnitKerjaRuang,
	)...)
}

func scanStokOpnameDetail(row rowScanner, detail *entity.StokOpnameDetail) error {
	return row.Scan(
		&detail.ID, &detail.IDStokOpname, &detail.IDBarang, &detail.IDRuang,
		&detail.StokAwal, &detail.StokSO, &detail.StokSelisihLebih, &detail.StokSelisihKurang,
		&detail.Keterangan, &detail.IDKartuStokCutoff, &detail.IDKartuStokPenyesuaian,
		&detail.UpdatedBy, &detail.TsUpdate,
		&detail.KodeBarang, &detail.NamaProduct, &detail.NamaSatuanDasar,
	)
}
