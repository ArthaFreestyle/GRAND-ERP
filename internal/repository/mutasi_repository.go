package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// MutasiRepository owns every statement touching mutasi and mutasi_detail.
type MutasiRepository struct{}

func NewMutasiRepository() *MutasiRepository {
	return &MutasiRepository{}
}

// mutasiColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
//
// No money column appears, because the table has none. What a mutasi is worth is
// decided per line by the kartu_stok trigger at posting; nothing about it belongs on
// the header.
const mutasiColumns = `id, nomor, tanggal, id_ruang_asal, id_ruang_tujuan, keterangan,
	status, created_by, created_at, posted_at, dibatalkan_oleh, alasan_batal`

// mutasiReadColumns adds both room names. Fetching them per row would be an N+1.
const mutasiReadColumns = `m.id, m.nomor, m.tanggal, m.id_ruang_asal, m.id_ruang_tujuan,
	m.keterangan, m.status, m.created_by, m.created_at, m.posted_at,
	m.dibatalkan_oleh, m.alasan_batal, asal.nama_ruang, tujuan.nama_ruang`

// mutasiFrom joins INNER twice: both foreign keys are NOT NULL, so a mutasi without a
// source or destination room cannot exist.
const mutasiFrom = `
	FROM mutasi m
	JOIN ruang asal ON asal.id = m.id_ruang_asal
	JOIN ruang tujuan ON tujuan.id = m.id_ruang_tujuan`

// mutasiFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$6 and pagination follows after it.
const mutasiFilter = `
	WHERE ($1 = '' OR m.nomor ILIKE '%' || $1 || '%' OR m.keterangan ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR m.status = $2)
	  AND ($3 = 0 OR m.id_ruang_asal = $3)
	  AND ($4 = 0 OR m.id_ruang_tujuan = $4)
	  AND ($5::DATE IS NULL OR m.tanggal >= $5::DATE)
	  AND ($6::DATE IS NULL OR m.tanggal < ($6::DATE + INTERVAL '1 day'))`

const mutasiDetailReadColumns = `md.id, md.id_mutasi, md.id_product, md.qty_input::TEXT,
	md.id_satuan_input, md.faktor_konversi, md.qty_dasar,
	md.harga_pokok_satuan_dasar::TEXT,
	pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const mutasiDetailFrom = `
	FROM mutasi_detail md
	JOIN product pr ON pr.id = md.id_product
	JOIN satuan si ON si.id = md.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// MutasiPatch carries a partial header update. Both rooms are here, unlike
// PembayaranUtangPatch which keeps id_supplier out: an allocation points at a specific
// supplier's invoices, but no mutasi_detail row points at a room. The rooms live on the
// header alone, so changing them leaves nothing behind pointing at the wrong place.
type MutasiPatch struct {
	Tanggal       *time.Time
	IDRuangAsal   *int64
	IDRuangTujuan *int64
	SetKeterangan bool
	Keterangan    *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can reach
// the joined names.
func (r *MutasiRepository) Create(ctx context.Context, db DBTX, mutasi *entity.Mutasi) error {
	const query = `
		INSERT INTO mutasi (
			nomor, tanggal, id_ruang_asal, id_ruang_tujuan, keterangan, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		mutasi.Nomor, mutasi.Tanggal, mutasi.IDRuangAsal, mutasi.IDRuangTujuan,
		mutasi.Keterangan, mutasi.Status, mutasi.CreatedBy,
	).Scan(&mutasi.ID, &mutasi.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert mutasi: %w", err)
	}

	return nil
}

func (r *MutasiRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Mutasi, error) {
	const query = `SELECT ` + mutasiReadColumns + mutasiFrom + ` WHERE m.id = $1`

	mutasi := new(entity.Mutasi)

	if err := scanMutasiRead(db.QueryRowContext(ctx, query, id), mutasi); err != nil {
		return nil, err
	}

	return mutasi, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a state
// transition cannot race another one. See PembelianRepository.LockByID.
func (r *MutasiRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.Mutasi, error) {
	const query = `SELECT ` + mutasiColumns + ` FROM mutasi WHERE id = $1 FOR UPDATE`

	mutasi := new(entity.Mutasi)

	if err := scanMutasi(db.QueryRowContext(ctx, query, id), mutasi); err != nil {
		return nil, err
	}

	return mutasi, nil
}

// UpdateHeader patches a DRAFT.
//
// mutasi_ruang_check rejects a source equal to the destination, and it has to be
// checked against the stored row rather than the patch: changing only one of the two
// can collide with the other one already there. The usecase reads the locked row first
// so the message can name the field; this is the backstop, and it arrives as SQLSTATE
// 23514.
func (r *MutasiRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch MutasiPatch) error {
	const query = `
		UPDATE mutasi SET
			tanggal         = COALESCE($2, tanggal),
			id_ruang_asal   = COALESCE($3, id_ruang_asal),
			id_ruang_tujuan = COALESCE($4, id_ruang_tujuan),
			keterangan      = CASE WHEN $5::BOOLEAN THEN $6 ELSE keterangan END
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id, patch.Tanggal, patch.IDRuangAsal, patch.IDRuangTujuan,
		patch.SetKeterangan, patch.Keterangan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// Posting closes the document. Called after both kartu_stok rows of every line are
// written, in the same transaction, so a failure at either end leaves neither.
//
// No disetujui_oleh: there is no DIAJUKAN to approve. Who was allowed to call this is
// the route guard's answer, and created_by already records who typed the document.
func (r *MutasiRepository) Posting(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE mutasi SET
			posted_at = now(),
			status    = 'POSTED'
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "posting mutasi", id)
}

// Batal marks a posted document void. The reversing kartu_stok rows are written by the
// usecase in the same transaction; nothing is deleted anywhere.
func (r *MutasiRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE mutasi SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal mutasi", id, actorID, alasan)
}

// Search returns one page plus the total matching count.
//
// terlamaDulu flips the ordering to oldest first. That is what the DRAFT queue uses:
// without a DIAJUKAN state there is no "this one is ready" signal, so the list of
// DRAFTs is the whole of SUPERADMIN's work queue — and a queue is read to be worked
// through, not to see the newest. GET /supplier/{id}/utang made the same choice for the
// same reason.
func (r *MutasiRepository) Search(ctx context.Context, db DBTX, search, status string, idRuangAsal, idRuangTujuan int64, dari, sampai *string, terlamaDulu bool, limit, offset int) ([]entity.Mutasi, int64, error) {
	search = EscapeLike(search)

	// COUNT skips the joins: nothing in the filter needs the room names.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM mutasi m`+mutasiFilter,
		search, status, idRuangAsal, idRuangTujuan, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count mutasi: %w", err)
	}

	if total == 0 {
		return []entity.Mutasi{}, 0, nil
	}

	// Either way the ORDER BY ends in a unique column, so a page boundary between
	// same-day documents cannot repeat or skip one. mutasi_tanggal_id_idx from migration
	// 000018 supports the descending form directly and the ascending one by reverse scan.
	urutan := `ORDER BY m.tanggal DESC, m.id DESC`
	if terlamaDulu {
		urutan = `ORDER BY m.tanggal ASC, m.id ASC`
	}

	query := `SELECT ` + mutasiReadColumns + mutasiFrom + mutasiFilter + `
		` + urutan + `
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(ctx, query, search, status, idRuangAsal, idRuangTujuan, dari, sampai, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select mutasi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Mutasi, 0, limit)

	for rows.Next() {
		var mutasi entity.Mutasi

		if err := scanMutasiRead(rows, &mutasi); err != nil {
			return nil, 0, fmt.Errorf("scan mutasi: %w", err)
		}

		list = append(list, mutasi)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate mutasi: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
//
// harga_pokok_satuan_dasar is deliberately absent from the column list: a DRAFT has no
// answer for it. It is filled by UpdateDetailPosting from what the outgoing kartu_stok
// row's RETURNING reported.
//
// There is no unique index on (id_mutasi, id_product), unlike the three documents that
// draw down a quota held on a parent line. Here the quota is the source room's balance
// and the trigger checks it on every insert, so a second line for the same product is
// refused on its own if the stock is not there. Two lines for the same product in
// different input units are a legitimate way to type a transfer.
func (r *MutasiRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.MutasiDetail) error {
	const query = `
		INSERT INTO mutasi_detail (
			id_mutasi, id_product, qty_input, id_satuan_input, faktor_konversi, qty_dasar
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDMutasi, detail.IDProduct, detail.QtyInput, detail.IDSatuanInput,
		detail.FaktorKonversi, detail.QtyDasar,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert mutasi_detail: %w", err)
	}

	return nil
}

// UpdateDetailPosting records what the source room's stock actually cost, copied from
// the harga_pokok_satuan the outgoing kartu_stok row reported back.
//
// Written at posting rather than when the line is typed, because that is the first
// moment the figure exists. See MutasiDetail.HargaPokokSatuanDasar for why the
// application cannot compute it itself.
func (r *MutasiRepository) UpdateDetailPosting(ctx context.Context, db DBTX, id int64, hargaPokokSatuanDasar string) error {
	const query = `
		UPDATE mutasi_detail SET harga_pokok_satuan_dasar = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, hargaPokokSatuanDasar); err != nil {
		return fmt.Errorf("update mutasi_detail posting: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /mutasi/{id}/detail performs. Only ever reached on a DRAFT: a posted line is what
// two kartu_stok rows describe.
func (r *MutasiRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM mutasi_detail WHERE id_mutasi = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete mutasi_detail: %w", err)
	}

	return nil
}

func (r *MutasiRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.MutasiDetail, error) {
	const query = `SELECT ` + mutasiDetailReadColumns + mutasiDetailFrom + `
		WHERE md.id_mutasi = $1
		ORDER BY md.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select mutasi_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.MutasiDetail, 0, 8)

	for rows.Next() {
		var detail entity.MutasiDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDMutasi, &detail.IDProduct, &detail.QtyInput,
			&detail.IDSatuanInput, &detail.FaktorKonversi, &detail.QtyDasar,
			&detail.HargaPokokSatuanDasar,
			&detail.KodeBarang, &detail.NamaProduct, &detail.NamaSatuan,
			&detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan mutasi_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mutasi_detail: %w", err)
	}

	return list, nil
}

// mutasiFields lists the scan targets in the order of mutasiColumns, once, so the two
// read paths cannot drift apart from each other or from the constant.
func mutasiFields(mutasi *entity.Mutasi) []any {
	return []any{
		&mutasi.ID, &mutasi.Nomor, &mutasi.Tanggal,
		&mutasi.IDRuangAsal, &mutasi.IDRuangTujuan, &mutasi.Keterangan,
		&mutasi.Status, &mutasi.CreatedBy, &mutasi.CreatedAt, &mutasi.PostedAt,
		&mutasi.DibatalkanOleh, &mutasi.AlasanBatal,
	}
}

func scanMutasi(row rowScanner, mutasi *entity.Mutasi) error {
	return row.Scan(mutasiFields(mutasi)...)
}

func scanMutasiRead(row rowScanner, mutasi *entity.Mutasi) error {
	return row.Scan(append(
		mutasiFields(mutasi), &mutasi.NamaRuangAsal, &mutasi.NamaRuangTujuan,
	)...)
}
