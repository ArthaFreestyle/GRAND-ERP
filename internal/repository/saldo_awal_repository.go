package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// SaldoAwalRepository owns every statement touching saldo_awal and saldo_awal_detail.
type SaldoAwalRepository struct{}

func NewSaldoAwalRepository() *SaldoAwalRepository {
	return &SaldoAwalRepository{}
}

// saldoAwalColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
//
// total_nilai is cast to TEXT: NUMERIC(20,2), nullable, and scanning it into a float64
// would round the one figure that says how much inventory value this document created.
const saldoAwalColumns = `id, nomor, tanggal, id_ruang, alasan, status, total_nilai::TEXT,
	created_by, created_at, diajukan_oleh, diajukan_pada, disetujui_oleh, disetujui_pada,
	posted_at, dibatalkan_oleh, alasan_batal, alasan_tolak`

// saldoAwalReadColumns adds the room's name and its unit, which ride on the join the
// query already makes — isu #12 fase 6 read scoping costs one column, never a second
// query.
const saldoAwalReadColumns = `sa.id, sa.nomor, sa.tanggal, sa.id_ruang, sa.alasan, sa.status,
	sa.total_nilai::TEXT, sa.created_by, sa.created_at, sa.diajukan_oleh, sa.diajukan_pada,
	sa.disetujui_oleh, sa.disetujui_pada, sa.posted_at, sa.dibatalkan_oleh, sa.alasan_batal,
	sa.alasan_tolak, r.nama_ruang, r.id_unit_kerja`

// saldoAwalFrom is shared by the COUNT and the row query: the filter reaches ruang
// (the unit scope), so the COUNT has to join it too.
const saldoAwalFrom = `
	FROM saldo_awal sa
	JOIN ruang r ON r.id = sa.id_ruang`

// saldoAwalFilter is written once for both queries. It owns $1..$6 and pagination
// follows after it. $6 is the active-unit scope — NULL means unrestricted.
const saldoAwalFilter = `
	WHERE ($1 = '' OR sa.nomor ILIKE '%' || $1 || '%' OR sa.alasan ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR sa.status = $2)
	  AND ($3 = 0 OR sa.id_ruang = $3)
	  AND ($4::DATE IS NULL OR sa.tanggal >= $4::DATE)
	  AND ($5::DATE IS NULL OR sa.tanggal <= $5::DATE)
	  AND ($6::BIGINT IS NULL OR r.id_unit_kerja = $6)`

const saldoAwalDetailReadColumns = `d.id, d.id_saldo_awal, d.id_product, d.qty_input::TEXT,
	d.id_satuan_input, d.faktor_konversi, d.qty_dasar, d.harga_satuan_input::TEXT,
	d.harga_pokok_satuan_dasar::TEXT, d.nilai_masuk::TEXT, d.id_kartu_stok,
	pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const saldoAwalDetailFrom = `
	FROM saldo_awal_detail d
	JOIN product pr ON pr.id = d.id_product
	JOIN satuan si ON si.id = d.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// SaldoAwalPatch carries a partial header update. Every field is a NOT NULL column, so
// the usecase rejects an explicit null before this runs — COALESCE is right here.
type SaldoAwalPatch struct {
	Tanggal *time.Time
	IDRuang *int64
	Alasan  *string
}

// Create inserts the header and fills ID. The response is always re-read through
// FindByID, the only query that reaches the joined names.
func (r *SaldoAwalRepository) Create(ctx context.Context, db DBTX, saldoAwal *entity.SaldoAwal) error {
	const query = `
		INSERT INTO saldo_awal (nomor, tanggal, id_ruang, alasan, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		saldoAwal.Nomor, saldoAwal.Tanggal, saldoAwal.IDRuang, saldoAwal.Alasan,
		saldoAwal.Status, saldoAwal.CreatedBy,
	).Scan(&saldoAwal.ID, &saldoAwal.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert saldo_awal: %w", err)
	}

	return nil
}

func (r *SaldoAwalRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.SaldoAwal, error) {
	const query = `SELECT ` + saldoAwalReadColumns + saldoAwalFrom + ` WHERE sa.id = $1`

	saldoAwal := new(entity.SaldoAwal)

	if err := scanSaldoAwalRead(db.QueryRowContext(ctx, query, id), saldoAwal); err != nil {
		return nil, err
	}

	return saldoAwal, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *SaldoAwalRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.SaldoAwal, error) {
	const query = `SELECT ` + saldoAwalColumns + ` FROM saldo_awal WHERE id = $1 FOR UPDATE`

	saldoAwal := new(entity.SaldoAwal)

	if err := scanSaldoAwal(db.QueryRowContext(ctx, query, id), saldoAwal); err != nil {
		return nil, err
	}

	return saldoAwal, nil
}

// UpdateHeader patches a DRAFT. The caller has already taken the row lock and checked
// the status (kunciDenganStatus), so this carries no status guard of its own.
func (r *SaldoAwalRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch SaldoAwalPatch) error {
	const query = `
		UPDATE saldo_awal SET
			tanggal  = COALESCE($2, tanggal),
			id_ruang = COALESCE($3, id_ruang),
			alasan   = COALESCE($4, alasan)
		WHERE id = $1
		RETURNING id`

	var updated int64

	return db.QueryRowContext(ctx, query, id, patch.Tanggal, patch.IDRuang, patch.Alasan).Scan(&updated)
}

// Ajukan hands a draft to the approver. It clears alasan_tolak: a rejection note
// describes the previous submission, not this one.
func (r *SaldoAwalRepository) Ajukan(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE saldo_awal SET
			status        = 'DIAJUKAN',
			diajukan_oleh = $2,
			diajukan_pada = now(),
			alasan_tolak  = NULL
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan saldo_awal", id, actorID)
}

// Tolak sends a submission back to DRAFT with a reason. The submitter is cleared: the
// next submission is a new one, and leaving the old stamp would misdate it.
func (r *SaldoAwalRepository) Tolak(ctx context.Context, db DBTX, id int64, alasan string) error {
	const query = `
		UPDATE saldo_awal SET
			status        = 'DRAFT',
			alasan_tolak  = $2,
			diajukan_oleh = NULL,
			diajukan_pada = NULL
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak saldo_awal", id, alasan)
}

// Posting closes the document. Called after every kartu_stok row is written, in the
// same transaction, so a failure at either end leaves neither.
func (r *SaldoAwalRepository) Posting(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE saldo_awal SET
			status         = 'POSTED',
			disetujui_oleh = $2,
			disetujui_pada = now(),
			posted_at      = now()
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "posting saldo_awal", id, actorID)
}

// Batal marks a document void from any non-BATAL status. From DRAFT/DIAJUKAN nothing
// was ever written to kartu_stok, so this is a pure status change; from POSTED the
// usecase has appended the reversing rows in the same transaction.
func (r *SaldoAwalRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE saldo_awal SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status IN ('DRAFT', 'DIAJUKAN', 'POSTED')`

	return execTransisi(ctx, db, query, "batal saldo_awal", id, actorID, alasan)
}

// RecalculateTotalNilai writes the header's total, summed by the usecase from every
// line's nilai_masuk. Written once, right after the posting loop; nothing about a
// POSTED document's lines can change afterwards.
func (r *SaldoAwalRepository) RecalculateTotalNilai(ctx context.Context, db DBTX, id int64, totalNilai string) error {
	const query = `UPDATE saldo_awal SET total_nilai = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, totalNilai); err != nil {
		return fmt.Errorf("update saldo_awal total_nilai: %w", err)
	}

	return nil
}

// Search returns one page plus the total matching count. Newest first; the ORDER BY
// ends in a unique column so a page boundary between same-day documents cannot repeat
// or skip one.
func (r *SaldoAwalRepository) Search(ctx context.Context, db DBTX, search, status string, idRuang int64, dari, sampai *string, aktifIDUnitKerja *int64, limit, offset int) ([]entity.SaldoAwal, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+saldoAwalFrom+saldoAwalFilter,
		search, status, idRuang, dari, sampai, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count saldo_awal: %w", err)
	}

	if total == 0 {
		return []entity.SaldoAwal{}, 0, nil
	}

	query := `SELECT ` + saldoAwalReadColumns + saldoAwalFrom + saldoAwalFilter + `
		ORDER BY sa.tanggal DESC, sa.id DESC
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(ctx, query, search, status, idRuang, dari, sampai, aktifIDUnitKerja, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select saldo_awal: %w", err)
	}
	defer rows.Close()

	list := make([]entity.SaldoAwal, 0, limit)

	for rows.Next() {
		var saldoAwal entity.SaldoAwal

		if err := scanSaldoAwalRead(rows, &saldoAwal); err != nil {
			return nil, 0, fmt.Errorf("scan saldo_awal: %w", err)
		}

		list = append(list, saldoAwal)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate saldo_awal: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line. id_kartu_stok is deliberately absent: a DRAFT has no
// answer for it, and UpdateDetailKartuStok fills it at Posting from RETURNING.
func (r *SaldoAwalRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.SaldoAwalDetail) error {
	const query = `
		INSERT INTO saldo_awal_detail (
			id_saldo_awal, id_product, qty_input, id_satuan_input, faktor_konversi,
			qty_dasar, harga_satuan_input, harga_pokok_satuan_dasar, nilai_masuk
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDSaldoAwal, detail.IDProduct, detail.QtyInput, detail.IDSatuanInput,
		detail.FaktorKonversi, detail.QtyDasar, detail.HargaSatuanInput,
		detail.HargaPokokSatuanDasar, detail.NilaiMasuk,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert saldo_awal_detail: %w", err)
	}

	return nil
}

// UpdateDetailKartuStok records which incoming kartu_stok row a line produced.
func (r *SaldoAwalRepository) UpdateDetailKartuStok(ctx context.Context, db DBTX, id, idKartuStok int64) error {
	const query = `UPDATE saldo_awal_detail SET id_kartu_stok = $2 WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, idKartuStok); err != nil {
		return fmt.Errorf("update saldo_awal_detail id_kartu_stok: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace PUT .../detail performs.
// Only ever reached on a DRAFT.
func (r *SaldoAwalRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM saldo_awal_detail WHERE id_saldo_awal = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete saldo_awal_detail: %w", err)
	}

	return nil
}

func (r *SaldoAwalRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.SaldoAwalDetail, error) {
	const query = `SELECT ` + saldoAwalDetailReadColumns + saldoAwalDetailFrom + `
		WHERE d.id_saldo_awal = $1
		ORDER BY d.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select saldo_awal_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.SaldoAwalDetail, 0, 8)

	for rows.Next() {
		var detail entity.SaldoAwalDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDSaldoAwal, &detail.IDProduct, &detail.QtyInput,
			&detail.IDSatuanInput, &detail.FaktorKonversi, &detail.QtyDasar,
			&detail.HargaSatuanInput, &detail.HargaPokokSatuanDasar, &detail.NilaiMasuk,
			&detail.IDKartuStok,
			&detail.KodeBarang, &detail.NamaProduct, &detail.NamaSatuan, &detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan saldo_awal_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saldo_awal_detail: %w", err)
	}

	return list, nil
}

// saldoAwalFields lists the scan targets in the order of saldoAwalColumns, once, so the
// two read paths cannot drift apart from each other or from the constant.
func saldoAwalFields(s *entity.SaldoAwal) []any {
	return []any{
		&s.ID, &s.Nomor, &s.Tanggal, &s.IDRuang, &s.Alasan, &s.Status, &s.TotalNilai,
		&s.CreatedBy, &s.CreatedAt, &s.DiajukanOleh, &s.DiajukanPada,
		&s.DisetujuiOleh, &s.DisetujuiPada, &s.PostedAt,
		&s.DibatalkanOleh, &s.AlasanBatal, &s.AlasanTolak,
	}
}

func scanSaldoAwal(row rowScanner, saldoAwal *entity.SaldoAwal) error {
	return row.Scan(saldoAwalFields(saldoAwal)...)
}

func scanSaldoAwalRead(row rowScanner, saldoAwal *entity.SaldoAwal) error {
	return row.Scan(append(saldoAwalFields(saldoAwal), &saldoAwal.NamaRuang, &saldoAwal.IDUnitKerjaRuang)...)
}
