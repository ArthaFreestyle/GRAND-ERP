package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// ReturPembelianRepository owns every statement touching retur_pembelian and
// retur_pembelian_detail.
type ReturPembelianRepository struct{}

func NewReturPembelianRepository() *ReturPembelianRepository {
	return &ReturPembelianRepository{}
}

// returColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
const returColumns = `id, nomor, tanggal, id_pembelian, id_supplier, id_ruang,
	alasan, total::TEXT, nilai_kredit_utang::TEXT, status, created_by, created_at,
	diajukan_oleh, diajukan_pada, disetujui_oleh, disetujui_pada, posted_at,
	dibatalkan_oleh, alasan_batal, alasan_tolak`

// returReadColumns adds the source document's number and the supplier and room names.
// Fetching them per row would be an N+1.
const returReadColumns = `r.id, r.nomor, r.tanggal, r.id_pembelian, r.id_supplier,
	r.id_ruang, r.alasan, r.total::TEXT, r.nilai_kredit_utang::TEXT, r.status,
	r.created_by, r.created_at,
	r.diajukan_oleh, r.diajukan_pada, r.disetujui_oleh, r.disetujui_pada, r.posted_at,
	r.dibatalkan_oleh, r.alasan_batal, r.alasan_tolak, p.nomor, sup.nama, ru.nama_ruang,
	ru.id_unit_kerja`

// returFrom joins INNER throughout: every one of these foreign keys is NOT NULL, so a
// return without a purchase, a supplier, or a room cannot exist.
const returFrom = `
	FROM retur_pembelian r
	JOIN pembelian p ON p.id = r.id_pembelian
	JOIN supplier sup ON sup.id = r.id_supplier
	JOIN ruang ru ON ru.id = r.id_ruang`

// returFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$7 and pagination follows after it.
//
// $7 is the active-unit scope (isu #12 fase 6), against ru.id_unit_kerja —
// which is why the COUNT query now has to join ruang too, not just pembelian.
const returFilter = `
	WHERE ($1 = '' OR r.nomor ILIKE '%' || $1 || '%' OR p.nomor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR r.status = $2)
	  AND ($3 = 0 OR r.id_pembelian = $3)
	  AND ($4 = 0 OR r.id_supplier = $4)
	  AND ($5::DATE IS NULL OR r.tanggal >= $5::DATE)
	  AND ($6::DATE IS NULL OR r.tanggal < ($6::DATE + INTERVAL '1 day'))
	  AND ($7::BIGINT IS NULL OR ru.id_unit_kerja = $7)`

// returDetailReadColumns reaches the product through pembelian_detail: a return line
// names the invoice line the goods came in on, not the product directly.
const returDetailReadColumns = `rd.id, rd.id_retur_pembelian, rd.id_pembelian_detail,
	rd.qty_input::TEXT, rd.id_satuan_input, rd.faktor_konversi, rd.qty_dasar,
	rd.harga_pokok_satuan_dasar::TEXT, rd.nilai::TEXT,
	pd.id_product, pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const returDetailFrom = `
	FROM retur_pembelian_detail rd
	JOIN pembelian_detail pd ON pd.id = rd.id_pembelian_detail
	JOIN product pr ON pr.id = pd.id_product
	JOIN satuan si ON si.id = rd.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// ReturPatch carries a partial header update. Only what a DRAFT may change is here:
// id_pembelian is absent because the document is defined as a return against that
// purchase, and id_supplier and id_ruang are copies of the purchase's, not choices.
type ReturPatch struct {
	Tanggal   *time.Time
	SetAlasan bool
	Alasan    *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can reach
// the joined names.
func (r *ReturPembelianRepository) Create(ctx context.Context, db DBTX, retur *entity.ReturPembelian) error {
	const query = `
		INSERT INTO retur_pembelian (
			nomor, tanggal, id_pembelian, id_supplier, id_ruang, alasan, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		retur.Nomor, retur.Tanggal, retur.IDPembelian, retur.IDSupplier,
		retur.IDRuang, retur.Alasan, retur.Status, retur.CreatedBy,
	).Scan(&retur.ID, &retur.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert retur_pembelian: %w", err)
	}

	return nil
}

func (r *ReturPembelianRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.ReturPembelian, error) {
	const query = `SELECT ` + returReadColumns + returFrom + ` WHERE r.id = $1`

	retur := new(entity.ReturPembelian)

	if err := scanReturRead(db.QueryRowContext(ctx, query, id), retur); err != nil {
		return nil, err
	}

	return retur, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *ReturPembelianRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.ReturPembelian, error) {
	const query = `SELECT ` + returColumns + ` FROM retur_pembelian WHERE id = $1 FOR UPDATE`

	retur := new(entity.ReturPembelian)

	if err := scanRetur(db.QueryRowContext(ctx, query, id), retur); err != nil {
		return nil, err
	}

	return retur, nil
}

func (r *ReturPembelianRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch ReturPatch) error {
	const query = `
		UPDATE retur_pembelian SET
			tanggal = COALESCE($2, tanggal),
			alasan  = CASE WHEN $3::BOOLEAN THEN $4 ELSE alasan END
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id, patch.Tanggal, patch.SetAlasan, patch.Alasan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// RecalculateTotal rewrites the inventory value the return withdraws, from the lines.
// Never set from a form.
func (r *ReturPembelianRepository) RecalculateTotal(ctx context.Context, db DBTX, id int64, total string) error {
	const query = `UPDATE retur_pembelian SET total = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, total); err != nil {
		return fmt.Errorf("recalculate retur_pembelian: %w", err)
	}

	return nil
}

// SetNilaiKreditUtang freezes what the supplier is credited, at posting.
//
// Separate from RecalculateTotal because the two figures answer different questions and
// are settled at different moments: `total` is the inventory value and is known as soon as
// the lines exist, while the credit against the payable is only meaningful once the
// document actually posts. A DRAFT credits nobody.
func (r *ReturPembelianRepository) SetNilaiKreditUtang(ctx context.Context, db DBTX, id int64, nilai string) error {
	const query = `UPDATE retur_pembelian SET nilai_kredit_utang = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, nilai); err != nil {
		return fmt.Errorf("set nilai_kredit_utang: %w", err)
	}

	return nil
}

// SumKreditUtangPosted totals what every POSTED return has already credited against a
// purchase, excluding one document.
//
// The exclusion is what makes it usable from inside the posting path of the document being
// excluded: it answers "how much credit is already committed by everyone else", which is
// what the new document's share has to fit inside.
func (r *ReturPembelianRepository) SumKreditUtangPosted(ctx context.Context, db DBTX, idPembelian, kecualiID int64) (string, error) {
	const query = `
		SELECT COALESCE(SUM(nilai_kredit_utang), 0)::NUMERIC(20, 2)::TEXT
		FROM retur_pembelian
		WHERE id_pembelian = $1 AND status = 'POSTED' AND id <> $2`

	var jumlah string
	if err := db.QueryRowContext(ctx, query, idPembelian, kecualiID).Scan(&jumlah); err != nil {
		return "", fmt.Errorf("sum nilai_kredit_utang: %w", err)
	}

	return jumlah, nil
}

func (r *ReturPembelianRepository) Ajukan(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE retur_pembelian SET
			status        = 'DIAJUKAN',
			diajukan_oleh = $2,
			diajukan_pada = now(),
			alasan_tolak  = NULL
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan retur_pembelian", id, actorID)
}

func (r *ReturPembelianRepository) Tolak(ctx context.Context, db DBTX, id int64, alasan string) error {
	const query = `
		UPDATE retur_pembelian SET
			status        = 'DRAFT',
			alasan_tolak  = $2,
			diajukan_oleh = NULL,
			diajukan_pada = NULL
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak retur_pembelian", id, alasan)
}

func (r *ReturPembelianRepository) Posting(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE retur_pembelian SET
			status         = 'POSTED',
			disetujui_oleh = $2,
			disetujui_pada = now(),
			posted_at      = now()
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "posting retur_pembelian", id, actorID)
}

func (r *ReturPembelianRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE retur_pembelian SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal retur_pembelian", id, actorID, alasan)
}

func (r *ReturPembelianRepository) Search(ctx context.Context, db DBTX, search, status string, idPembelian, idSupplier int64, dari, sampai *string, aktifIDUnitKerja *int64, limit, offset int) ([]entity.ReturPembelian, int64, error) {
	search = EscapeLike(search)

	// COUNT now runs through returFrom rather than a hand-rolled join, since $7
	// needs ru.id_unit_kerja alongside p.nomor.
	var total int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) `+returFrom+returFilter,
		search, status, idPembelian, idSupplier, dari, sampai, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count retur_pembelian: %w", err)
	}

	if total == 0 {
		return []entity.ReturPembelian{}, 0, nil
	}

	// ORDER BY ends in a unique column, so a page boundary between same-day documents
	// cannot repeat or skip one. retur_pembelian_tanggal_id_idx from migration 000014
	// supports exactly this.
	query := `SELECT ` + returReadColumns + returFrom + returFilter + `
		ORDER BY r.tanggal DESC, r.id DESC
		LIMIT $8 OFFSET $9`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, idPembelian, idSupplier, dari, sampai, aktifIDUnitKerja, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select retur_pembelian: %w", err)
	}
	defer rows.Close()

	list := make([]entity.ReturPembelian, 0, limit)

	for rows.Next() {
		var retur entity.ReturPembelian

		if err := scanReturRead(rows, &retur); err != nil {
			return nil, 0, fmt.Errorf("scan retur_pembelian: %w", err)
		}

		list = append(list, retur)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate retur_pembelian: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
//
// A repeat of the same pembelian_detail inside one document raises 23505 from
// retur_pembelian_detail_baris_uidx. That index matters: without it two lines for the
// same source would each pass the returnable-quantity check on their own and together
// exceed it.
func (r *ReturPembelianRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.ReturPembelianDetail) error {
	const query = `
		INSERT INTO retur_pembelian_detail (
			id_retur_pembelian, id_pembelian_detail, qty_input, id_satuan_input,
			faktor_konversi, qty_dasar, harga_pokok_satuan_dasar, nilai
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDReturPembelian, detail.IDPembelianDetail, detail.QtyInput,
		detail.IDSatuanInput, detail.FaktorKonversi, detail.QtyDasar,
		detail.HargaPokokSatuanDasar, detail.Nilai,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert retur_pembelian_detail: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /retur-pembelian/{id}/detail performs. Only ever reached on a DRAFT: a posted
// line is what the returnable quantity is computed from.
func (r *ReturPembelianRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM retur_pembelian_detail WHERE id_retur_pembelian = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete retur_pembelian_detail: %w", err)
	}

	return nil
}

func (r *ReturPembelianRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.ReturPembelianDetail, error) {
	const query = `SELECT ` + returDetailReadColumns + returDetailFrom + `
		WHERE rd.id_retur_pembelian = $1
		ORDER BY rd.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select retur_pembelian_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.ReturPembelianDetail, 0, 8)

	for rows.Next() {
		var detail entity.ReturPembelianDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDReturPembelian, &detail.IDPembelianDetail,
			&detail.QtyInput, &detail.IDSatuanInput, &detail.FaktorKonversi,
			&detail.QtyDasar, &detail.HargaPokokSatuanDasar, &detail.Nilai,
			&detail.IDProduct, &detail.KodeBarang, &detail.NamaProduct,
			&detail.NamaSatuan, &detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan retur_pembelian_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate retur_pembelian_detail: %w", err)
	}

	return list, nil
}

// returFields lists the scan targets in the order of returColumns, once, so the two
// read paths cannot drift apart from each other or from the constant.
func returFields(retur *entity.ReturPembelian) []any {
	return []any{
		&retur.ID, &retur.Nomor, &retur.Tanggal, &retur.IDPembelian,
		&retur.IDSupplier, &retur.IDRuang, &retur.Alasan,
		&retur.Total, &retur.NilaiKreditUtang, &retur.Status,
		&retur.CreatedBy, &retur.CreatedAt,
		&retur.DiajukanOleh, &retur.DiajukanPada, &retur.DisetujuiOleh,
		&retur.DisetujuiPada, &retur.PostedAt,
		&retur.DibatalkanOleh, &retur.AlasanBatal, &retur.AlasanTolak,
	}
}

func scanRetur(row rowScanner, retur *entity.ReturPembelian) error {
	return row.Scan(returFields(retur)...)
}

func scanReturRead(row rowScanner, retur *entity.ReturPembelian) error {
	return row.Scan(append(
		returFields(retur),
		&retur.NomorPembelian, &retur.NamaSupplier, &retur.NamaRuang,
		&retur.IDUnitKerjaRuang,
	)...)
}
