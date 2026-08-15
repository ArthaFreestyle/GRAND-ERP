package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PenerimaanSusulanRepository owns every statement touching penerimaan_susulan and
// penerimaan_susulan_detail.
type PenerimaanSusulanRepository struct{}

func NewPenerimaanSusulanRepository() *PenerimaanSusulanRepository {
	return &PenerimaanSusulanRepository{}
}

// susulanColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
const susulanColumns = `id, nomor, tanggal, id_pembelian, id_supplier, id_ruang,
	keterangan, total_nilai::TEXT, status, created_by, created_at,
	diajukan_oleh, diajukan_pada, disetujui_oleh, disetujui_pada, posted_at,
	dibatalkan_oleh, alasan_batal, alasan_tolak`

// susulanReadColumns adds the source document's number and the supplier and room
// names. Fetching them per row would be an N+1.
const susulanReadColumns = `s.id, s.nomor, s.tanggal, s.id_pembelian, s.id_supplier,
	s.id_ruang, s.keterangan, s.total_nilai::TEXT, s.status, s.created_by, s.created_at,
	s.diajukan_oleh, s.diajukan_pada, s.disetujui_oleh, s.disetujui_pada, s.posted_at,
	s.dibatalkan_oleh, s.alasan_batal, s.alasan_tolak, p.nomor, sup.nama, r.nama_ruang,
	r.id_unit_kerja`

// susulanFrom joins INNER throughout: every one of these foreign keys is NOT NULL,
// so a follow-up receipt without a purchase, a supplier, or a room cannot exist.
const susulanFrom = `
	FROM penerimaan_susulan s
	JOIN pembelian p ON p.id = s.id_pembelian
	JOIN supplier sup ON sup.id = s.id_supplier
	JOIN ruang r ON r.id = s.id_ruang`

// susulanFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$6 and pagination follows after it.
//
// $6 is the active-unit scope (isu #12 fase 6), against r.id_unit_kerja — which
// is why the COUNT query now has to join ruang too, not just pembelian.
const susulanFilter = `
	WHERE ($1 = '' OR s.nomor ILIKE '%' || $1 || '%' OR p.nomor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR s.status = $2)
	  AND ($3 = 0 OR s.id_pembelian = $3)
	  AND ($4::DATE IS NULL OR s.tanggal >= $4::DATE)
	  AND ($5::DATE IS NULL OR s.tanggal < ($5::DATE + INTERVAL '1 day'))
	  AND ($6::BIGINT IS NULL OR r.id_unit_kerja = $6)`

// susulanDetailReadColumns reaches the product through pembelian_detail: a
// follow-up line names the invoice line it completes, not the product directly, so
// the product is whatever that line was for.
const susulanDetailReadColumns = `sd.id, sd.id_penerimaan_susulan, sd.id_pembelian_detail,
	sd.qty_input::TEXT, sd.id_satuan_input, sd.faktor_konversi, sd.qty_dasar,
	sd.harga_pokok_satuan_dasar::TEXT, sd.nilai::TEXT,
	pd.id_product, pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const susulanDetailFrom = `
	FROM penerimaan_susulan_detail sd
	JOIN pembelian_detail pd ON pd.id = sd.id_pembelian_detail
	JOIN product pr ON pr.id = pd.id_product
	JOIN satuan si ON si.id = sd.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// SusulanPatch carries a partial header update. Only what a DRAFT may change is
// here: id_pembelian is absent because the document is defined as the remainder of
// that purchase, and id_supplier and id_ruang are copies of the purchase's, not
// choices.
type SusulanPatch struct {
	Tanggal       *time.Time
	SetKeterangan bool
	Keterangan    *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can
// reach the joined names.
func (r *PenerimaanSusulanRepository) Create(ctx context.Context, db DBTX, susulan *entity.PenerimaanSusulan) error {
	const query = `
		INSERT INTO penerimaan_susulan (
			nomor, tanggal, id_pembelian, id_supplier, id_ruang, keterangan, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		susulan.Nomor, susulan.Tanggal, susulan.IDPembelian, susulan.IDSupplier,
		susulan.IDRuang, susulan.Keterangan, susulan.Status, susulan.CreatedBy,
	).Scan(&susulan.ID, &susulan.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert penerimaan_susulan: %w", err)
	}

	return nil
}

func (r *PenerimaanSusulanRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.PenerimaanSusulan, error) {
	const query = `SELECT ` + susulanReadColumns + susulanFrom + ` WHERE s.id = $1`

	susulan := new(entity.PenerimaanSusulan)

	if err := scanSusulanRead(db.QueryRowContext(ctx, query, id), susulan); err != nil {
		return nil, err
	}

	return susulan, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *PenerimaanSusulanRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.PenerimaanSusulan, error) {
	const query = `SELECT ` + susulanColumns + ` FROM penerimaan_susulan WHERE id = $1 FOR UPDATE`

	susulan := new(entity.PenerimaanSusulan)

	if err := scanSusulan(db.QueryRowContext(ctx, query, id), susulan); err != nil {
		return nil, err
	}

	return susulan, nil
}

func (r *PenerimaanSusulanRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch SusulanPatch) error {
	const query = `
		UPDATE penerimaan_susulan SET
			tanggal    = COALESCE($2, tanggal),
			keterangan = CASE WHEN $3::BOOLEAN THEN $4 ELSE keterangan END
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id, patch.Tanggal, patch.SetKeterangan, patch.Keterangan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// RecalculateTotal rewrites the value of the goods that arrived late, from the
// lines. Not a payable — the invoice was booked in full with the first delivery.
func (r *PenerimaanSusulanRepository) RecalculateTotal(ctx context.Context, db DBTX, id int64, totalNilai string) error {
	const query = `UPDATE penerimaan_susulan SET total_nilai = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, totalNilai); err != nil {
		return fmt.Errorf("recalculate penerimaan_susulan: %w", err)
	}

	return nil
}

func (r *PenerimaanSusulanRepository) Ajukan(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE penerimaan_susulan SET
			status        = 'DIAJUKAN',
			diajukan_oleh = $2,
			diajukan_pada = now(),
			alasan_tolak  = NULL
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan penerimaan_susulan", id, actorID)
}

func (r *PenerimaanSusulanRepository) Tolak(ctx context.Context, db DBTX, id int64, alasan string) error {
	const query = `
		UPDATE penerimaan_susulan SET
			status        = 'DRAFT',
			alasan_tolak  = $2,
			diajukan_oleh = NULL,
			diajukan_pada = NULL
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak penerimaan_susulan", id, alasan)
}

func (r *PenerimaanSusulanRepository) Posting(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE penerimaan_susulan SET
			status         = 'POSTED',
			disetujui_oleh = $2,
			disetujui_pada = now(),
			posted_at      = now()
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "posting penerimaan_susulan", id, actorID)
}

func (r *PenerimaanSusulanRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE penerimaan_susulan SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal penerimaan_susulan", id, actorID, alasan)
}

func (r *PenerimaanSusulanRepository) Search(ctx context.Context, db DBTX, search, status string, idPembelian int64, dari, sampai *string, aktifIDUnitKerja *int64, limit, offset int) ([]entity.PenerimaanSusulan, int64, error) {
	search = EscapeLike(search)

	// COUNT now runs through susulanFrom rather than a hand-rolled join, since
	// $6 needs r.id_unit_kerja alongside p.nomor.
	var total int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) `+susulanFrom+susulanFilter,
		search, status, idPembelian, dari, sampai, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count penerimaan_susulan: %w", err)
	}

	if total == 0 {
		return []entity.PenerimaanSusulan{}, 0, nil
	}

	// ORDER BY ends in a unique column, so a page boundary between same-day
	// documents cannot repeat or skip one.
	query := `SELECT ` + susulanReadColumns + susulanFrom + susulanFilter + `
		ORDER BY s.tanggal DESC, s.id DESC
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(
		ctx, query, search, status, idPembelian, dari, sampai, aktifIDUnitKerja, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select penerimaan_susulan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PenerimaanSusulan, 0, limit)

	for rows.Next() {
		var susulan entity.PenerimaanSusulan

		if err := scanSusulanRead(rows, &susulan); err != nil {
			return nil, 0, fmt.Errorf("scan penerimaan_susulan: %w", err)
		}

		list = append(list, susulan)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate penerimaan_susulan: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
//
// A repeat of the same pembelian_detail inside one document raises 23505 from
// penerimaan_susulan_detail_baris_uidx. That index matters: without it two lines for
// the same source would each pass the outstanding-quantity check on their own and
// together exceed it.
func (r *PenerimaanSusulanRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.PenerimaanSusulanDetail) error {
	const query = `
		INSERT INTO penerimaan_susulan_detail (
			id_penerimaan_susulan, id_pembelian_detail, qty_input, id_satuan_input,
			faktor_konversi, qty_dasar, harga_pokok_satuan_dasar, nilai
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDPenerimaanSusulan, detail.IDPembelianDetail, detail.QtyInput,
		detail.IDSatuanInput, detail.FaktorKonversi, detail.QtyDasar,
		detail.HargaPokokSatuanDasar, detail.Nilai,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert penerimaan_susulan_detail: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /penerimaan-susulan/{id}/detail performs. Only ever reached on a DRAFT: a
// posted line is what the outstanding quantity is computed from.
func (r *PenerimaanSusulanRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM penerimaan_susulan_detail WHERE id_penerimaan_susulan = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete penerimaan_susulan_detail: %w", err)
	}

	return nil
}

func (r *PenerimaanSusulanRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.PenerimaanSusulanDetail, error) {
	const query = `SELECT ` + susulanDetailReadColumns + susulanDetailFrom + `
		WHERE sd.id_penerimaan_susulan = $1
		ORDER BY sd.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select penerimaan_susulan_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PenerimaanSusulanDetail, 0, 8)

	for rows.Next() {
		var detail entity.PenerimaanSusulanDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDPenerimaanSusulan, &detail.IDPembelianDetail,
			&detail.QtyInput, &detail.IDSatuanInput, &detail.FaktorKonversi,
			&detail.QtyDasar, &detail.HargaPokokSatuanDasar, &detail.Nilai,
			&detail.IDProduct, &detail.KodeBarang, &detail.NamaProduct,
			&detail.NamaSatuan, &detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan penerimaan_susulan_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate penerimaan_susulan_detail: %w", err)
	}

	return list, nil
}

// susulanFields lists the scan targets in the order of susulanColumns, once, so the
// two read paths cannot drift apart from each other or from the constant.
func susulanFields(susulan *entity.PenerimaanSusulan) []any {
	return []any{
		&susulan.ID, &susulan.Nomor, &susulan.Tanggal, &susulan.IDPembelian,
		&susulan.IDSupplier, &susulan.IDRuang, &susulan.Keterangan,
		&susulan.TotalNilai, &susulan.Status, &susulan.CreatedBy, &susulan.CreatedAt,
		&susulan.DiajukanOleh, &susulan.DiajukanPada, &susulan.DisetujuiOleh,
		&susulan.DisetujuiPada, &susulan.PostedAt,
		&susulan.DibatalkanOleh, &susulan.AlasanBatal, &susulan.AlasanTolak,
	}
}

func scanSusulan(row rowScanner, susulan *entity.PenerimaanSusulan) error {
	return row.Scan(susulanFields(susulan)...)
}

func scanSusulanRead(row rowScanner, susulan *entity.PenerimaanSusulan) error {
	return row.Scan(append(
		susulanFields(susulan),
		&susulan.NomorPembelian, &susulan.NamaSupplier, &susulan.NamaRuang,
		&susulan.IDUnitKerjaRuang,
	)...)
}
