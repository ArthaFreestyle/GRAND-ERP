package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PembelianRepository owns every statement touching pembelian and
// pembelian_detail. The two live together because every write to a detail row is
// driven by its header.
type PembelianRepository struct{}

func NewPembelianRepository() *PembelianRepository {
	return &PembelianRepository{}
}

// pembelianColumns is the unqualified list, for statements that cannot reach the
// joined tables — INSERT ... RETURNING and the SELECT ... FOR UPDATE.
//
// Every NUMERIC is cast to TEXT. NUMERIC(20,2) scanned into a float64 rounds money,
// and these figures become a payable and an inventory valuation.
const pembelianColumns = `id, nomor, tanggal, id_supplier, id_ruang, no_faktur_supplier,
	tanggal_faktur, subtotal::TEXT, diskon_nota::TEXT, ppn::TEXT, ppn_dikreditkan,
	pembulatan::TEXT, total::TEXT, id_ekspedisi, no_resi, total_koli::TEXT,
	tarif_per_koli::TEXT, biaya_angkut::TEXT, ditanggung_supplier, metode_alokasi_angkut,
	jenis_pembayaran, status_pembayaran, status_penerimaan, status, created_by, created_at,
	diajukan_oleh, diajukan_pada, disetujui_oleh, disetujui_pada, posted_at,
	dibatalkan_oleh, alasan_batal, alasan_tolak`

// pembelianReadColumns adds the supplier and room names, resolved by the joins in
// pembelianFrom. Fetching them per row would be an N+1.
const pembelianReadColumns = `p.id, p.nomor, p.tanggal, p.id_supplier, p.id_ruang,
	p.no_faktur_supplier, p.tanggal_faktur, p.subtotal::TEXT, p.diskon_nota::TEXT,
	p.ppn::TEXT, p.ppn_dikreditkan, p.pembulatan::TEXT, p.total::TEXT, p.id_ekspedisi,
	p.no_resi, p.total_koli::TEXT, p.tarif_per_koli::TEXT, p.biaya_angkut::TEXT,
	p.ditanggung_supplier, p.metode_alokasi_angkut, p.jenis_pembayaran,
	p.status_pembayaran, p.status_penerimaan, p.status, p.created_by, p.created_at,
	p.diajukan_oleh, p.diajukan_pada, p.disetujui_oleh, p.disetujui_pada, p.posted_at,
	p.dibatalkan_oleh, p.alasan_batal, p.alasan_tolak, s.nama, r.nama_ruang`

// pembelianFrom joins INNER: id_supplier and id_ruang are both NOT NULL and both
// carry a foreign key, so a purchase without either cannot exist. An outer join
// would only suggest otherwise.
const pembelianFrom = `
	FROM pembelian p
	JOIN supplier s ON s.id = p.id_supplier
	JOIN ruang r ON r.id = p.id_ruang`

// pembelianFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$6 and pagination follows after it.
//
// The date range is inclusive at both ends even though tanggal is TIMESTAMPTZ: an
// operator asking for 1 August to 31 August means the whole of the 31st, so the
// upper bound is `< the next day` rather than `<= the day`, which would cut the
// last day off at midnight.
const pembelianFilter = `
	WHERE ($1 = '' OR p.nomor ILIKE '%' || $1 || '%'
	                OR p.no_faktur_supplier ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR p.status = $2)
	  AND ($3 = '' OR p.status_penerimaan = $3)
	  AND ($4 = 0 OR p.id_supplier = $4)
	  AND ($5::DATE IS NULL OR p.tanggal >= $5::DATE)
	  AND ($6::DATE IS NULL OR p.tanggal < ($6::DATE + INTERVAL '1 day'))`

// pembelianDetailReadColumns joins three names onto each line. satuan appears
// twice: once for the unit the operator typed in, once for the product's base unit,
// because a line shows both ("2 DUS = 24 PCS").
const pembelianDetailReadColumns = `d.id, d.id_pembelian, d.id_product, d.qty_faktur::TEXT,
	d.qty_diterima::TEXT, d.id_satuan_input, d.faktor_konversi, d.qty_dasar,
	d.qty_diterima_dasar, ` + pembelianDetailSusulan + `, d.harga_satuan_input::TEXT,
	d.diskon_baris::TEXT, d.subtotal::TEXT, d.jumlah_koli::TEXT, d.alokasi_biaya::TEXT,
	d.harga_pokok_satuan_dasar::TEXT, d.keterangan_selisih,
	pr.nama, pr.kode_barang, si.nama, sd.nama`

// pembelianDetailSusulan totals the follow-up receipts already posted against a
// line. Declared once because the outstanding quantity is computed from it in three
// places, and three copies of this predicate would drift.
//
// A correlated subquery rather than a grouped LEFT JOIN: it is driven by the outer
// rows and uses penerimaan_susulan_detail_pembelian_detail_idx, so it costs one
// index lookup per line instead of aggregating the whole table before joining.
//
// Only POSTED documents count. A draft follow-up is not a delivery, and letting one
// reduce the outstanding figure would let an abandoned draft hide a real shortfall.
const pembelianDetailSusulan = `COALESCE((
		SELECT SUM(ps_d.qty_dasar)
		FROM penerimaan_susulan_detail ps_d
		JOIN penerimaan_susulan ps ON ps.id = ps_d.id_penerimaan_susulan
		WHERE ps_d.id_pembelian_detail = d.id AND ps.status = 'POSTED'
	), 0)`

const pembelianDetailFrom = `
	FROM pembelian_detail d
	JOIN product pr ON pr.id = d.id_product
	JOIN satuan si ON si.id = d.id_satuan_input
	JOIN satuan sd ON sd.id = pr.id_satuan_dasar`

// PembelianPatch carries a partial header update. Only fields a DRAFT may change
// appear: nomor, id_supplier, and id_ruang are absent because they identify the
// document and its destination, and every computed figure already stored would have
// to be recomputed against a different room's balance.
//
// Each nullable column gets a Set flag answering "was this key in the JSON body at
// all?" — without it, clearing a column is indistinguishable from leaving it alone.
type PembelianPatch struct {
	Tanggal             *time.Time
	SetNoFakturSupplier bool
	NoFakturSupplier    *string
	SetTanggalFaktur    bool
	TanggalFaktur       *time.Time
	DiskonNota          *string
	PPN                 *string
	PPNDikreditkan      *bool
	Pembulatan          *string
	SetIDEkspedisi      bool
	IDEkspedisi         *int64
	SetNoResi           bool
	NoResi              *string
	SetTotalKoli        bool
	TotalKoli           *string
	SetTarifPerKoli     bool
	TarifPerKoli        *string
	DitanggungSupplier  *bool
	MetodeAlokasiAngkut *string
	JenisPembayaran     *string
}

// Create inserts the header and fills ID.
//
// It returns only the generated key: the response is always re-read through
// FindByID, which is the only query that can reach the joined supplier and room
// names. Returning the full column list here as well would be a second place for
// the scan order to drift out of step.
func (r *PembelianRepository) Create(ctx context.Context, db DBTX, pembelian *entity.Pembelian) error {
	const query = `
		INSERT INTO pembelian (
			nomor, tanggal, id_supplier, id_ruang, no_faktur_supplier, tanggal_faktur,
			subtotal, diskon_nota, ppn, ppn_dikreditkan, pembulatan, total,
			id_ekspedisi, no_resi, total_koli, tarif_per_koli, biaya_angkut,
			ditanggung_supplier, metode_alokasi_angkut, jenis_pembayaran, status, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		        $17, $18, $19, $20, $21, $22)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		pembelian.Nomor, pembelian.Tanggal, pembelian.IDSupplier, pembelian.IDRuang,
		pembelian.NoFakturSupplier, pembelian.TanggalFaktur,
		pembelian.Subtotal, pembelian.DiskonNota, pembelian.PPN, pembelian.PPNDikreditkan,
		pembelian.Pembulatan, pembelian.Total,
		pembelian.IDEkspedisi, pembelian.NoResi, pembelian.TotalKoli, pembelian.TarifPerKoli,
		pembelian.BiayaAngkut, pembelian.DitanggungSupplier, pembelian.MetodeAlokasiAngkut,
		pembelian.JenisPembayaran, pembelian.Status, pembelian.CreatedBy,
	).Scan(&pembelian.ID, &pembelian.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pembelian: %w", err)
	}

	return nil
}

// FindByID returns the header with its supplier and room names. Details are not
// included; the caller adds them with FindDetail.
func (r *PembelianRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Pembelian, error) {
	const query = `SELECT ` + pembelianReadColumns + pembelianFrom + ` WHERE p.id = $1`

	pembelian := new(entity.Pembelian)

	if err := scanPembelianRead(db.QueryRowContext(ctx, query, id), pembelian); err != nil {
		return nil, err
	}

	return pembelian, nil
}

// LockByID reads the header and holds a row lock until the transaction ends.
//
// Every state transition reads the current status, decides, and writes — and
// without the lock two concurrent posting requests both read DIAJUKAN, both decide
// the transition is legal, and both write kartu_stok rows. Stock doubles, and
// kartu_stok is append-only, so that cannot be undone.
//
// No joins: FOR UPDATE cannot lock through one without naming the table, and the
// names are not needed to make a decision. The caller re-reads via FindByID for the
// response.
func (r *PembelianRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.Pembelian, error) {
	const query = `SELECT ` + pembelianColumns + ` FROM pembelian WHERE id = $1 FOR UPDATE`

	pembelian := new(entity.Pembelian)

	if err := scanPembelian(db.QueryRowContext(ctx, query, id), pembelian); err != nil {
		return nil, err
	}

	return pembelian, nil
}

// ExistsFakturSupplier mirrors pembelian_supplier_faktur_uidx.
//
// Without a purchase order, this document is the only trace of the supplier's
// invoice. Entering the same paper twice would raise stock twice, and that is not
// repairable — only reversible. Cancelled documents are excluded so a mistyped nota
// can be voided and re-entered under the same number.
func (r *PembelianRepository) ExistsFakturSupplier(ctx context.Context, db DBTX, idSupplier int64, noFaktur string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM pembelian
			WHERE id_supplier = $1
			  AND lower(no_faktur_supplier) = lower($2)
			  AND status <> 'BATAL'
			  AND ($3 = 0 OR id <> $3)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, idSupplier, noFaktur, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check no_faktur_supplier: %w", err)
	}

	return exists, nil
}

// UpdateHeader applies a patch. sql.ErrNoRows means the id does not exist, so there
// is no separate existence check.
//
// The nullable columns use `CASE WHEN $n::BOOLEAN THEN $m ELSE col END` rather than
// COALESCE, because COALESCE cannot tell "field absent" from "field explicitly
// null" and would leave an operator unable to clear a mistyped resi.
func (r *PembelianRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch PembelianPatch) error {
	const query = `
		UPDATE pembelian SET
			tanggal               = COALESCE($2, tanggal),
			no_faktur_supplier    = CASE WHEN $3::BOOLEAN THEN $4 ELSE no_faktur_supplier END,
			tanggal_faktur        = CASE WHEN $5::BOOLEAN THEN $6 ELSE tanggal_faktur END,
			diskon_nota           = COALESCE($7::NUMERIC, diskon_nota),
			ppn                   = COALESCE($8::NUMERIC, ppn),
			ppn_dikreditkan       = COALESCE($9, ppn_dikreditkan),
			pembulatan            = COALESCE($10::NUMERIC, pembulatan),
			id_ekspedisi          = CASE WHEN $11::BOOLEAN THEN $12 ELSE id_ekspedisi END,
			no_resi               = CASE WHEN $13::BOOLEAN THEN $14 ELSE no_resi END,
			total_koli            = CASE WHEN $15::BOOLEAN THEN $16::NUMERIC ELSE total_koli END,
			tarif_per_koli        = CASE WHEN $17::BOOLEAN THEN $18::NUMERIC ELSE tarif_per_koli END,
			ditanggung_supplier   = COALESCE($19, ditanggung_supplier),
			metode_alokasi_angkut = COALESCE($20, metode_alokasi_angkut),
			jenis_pembayaran      = COALESCE($21, jenis_pembayaran)
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Tanggal,
		patch.SetNoFakturSupplier, patch.NoFakturSupplier,
		patch.SetTanggalFaktur, patch.TanggalFaktur,
		patch.DiskonNota, patch.PPN, patch.PPNDikreditkan, patch.Pembulatan,
		patch.SetIDEkspedisi, patch.IDEkspedisi,
		patch.SetNoResi, patch.NoResi,
		patch.SetTotalKoli, patch.TotalKoli,
		patch.SetTarifPerKoli, patch.TarifPerKoli,
		patch.DitanggungSupplier, patch.MetodeAlokasiAngkut, patch.JenisPembayaran,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// RecalculateHeader writes the figures posting derives: the invoice totals rebuilt
// from the detail rows, the freight bill, and the receipt cache.
//
// biaya_angkut is stored but is NOT part of total. Total is what the supplier is
// owed; freight is billed by the carrier and reaches the books through
// alokasi_biaya on the lines instead. Folding it into total would inflate the
// payable by money that goes to someone else.
func (r *PembelianRepository) RecalculateHeader(ctx context.Context, db DBTX, id int64, subtotal, total, biayaAngkut, statusPenerimaan string) error {
	const query = `
		UPDATE pembelian SET
			subtotal          = $2::NUMERIC,
			total             = $3::NUMERIC,
			biaya_angkut      = $4::NUMERIC,
			status_penerimaan = $5
		WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, subtotal, total, biayaAngkut, statusPenerimaan); err != nil {
		return fmt.Errorf("recalculate pembelian: %w", err)
	}

	return nil
}

// Ajukan hands a draft over for approval. The status guard is in the WHERE clause
// as well as in the usecase: the caller already holds the row lock, and this makes
// the transition unable to fire from the wrong state even if that ever changes.
func (r *PembelianRepository) Ajukan(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE pembelian SET
			status        = 'DIAJUKAN',
			diajukan_oleh = $2,
			diajukan_pada = now(),
			alasan_tolak  = NULL
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "ajukan pembelian", id, actorID)
}

// Tolak sends a submission back to DRAFT with a reason, so the operator can fix it.
// The submitter is cleared: the next submission is a new one, and leaving the old
// stamp would misdate it.
func (r *PembelianRepository) Tolak(ctx context.Context, db DBTX, id int64, alasan string) error {
	const query = `
		UPDATE pembelian SET
			status        = 'DRAFT',
			alasan_tolak  = $2,
			diajukan_oleh = NULL,
			diajukan_pada = NULL
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "tolak pembelian", id, alasan)
}

// Posting closes the document. Called after the kartu_stok rows are written, in the
// same transaction, so a failure at either end leaves neither.
func (r *PembelianRepository) Posting(ctx context.Context, db DBTX, id, actorID int64) error {
	const query = `
		UPDATE pembelian SET
			status         = 'POSTED',
			disetujui_oleh = $2,
			disetujui_pada = now(),
			posted_at      = now()
		WHERE id = $1 AND status = 'DIAJUKAN'`

	return execTransisi(ctx, db, query, "posting pembelian", id, actorID)
}

// Batal marks a posted document void. The reversing kartu_stok rows are written by
// the usecase in the same transaction; nothing is deleted anywhere.
func (r *PembelianRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE pembelian SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal pembelian", id, actorID, alasan)
}

// execTransisi runs a guarded status change and reports whether it applied. Zero
// rows means the document moved out from under the caller between the lock and the
// write, which the usecase turns into a 409 rather than a silent success.
func execTransisi(ctx context.Context, db DBTX, query, what string, args ...any) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	if affected == 0 {
		return ErrTransisiStatus
	}

	return nil
}

// Search returns one page of headers plus the total matching count. Details are not
// attached: a page of 20 documents with their lines would be a second query per row
// for data the list view does not show.
func (r *PembelianRepository) Search(ctx context.Context, db DBTX, search, status, statusPenerimaan string, idSupplier int64, dari, sampai *string, limit, offset int) ([]entity.Pembelian, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM pembelian p`+pembelianFilter,
		search, status, statusPenerimaan, idSupplier, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count pembelian: %w", err)
	}

	if total == 0 {
		return []entity.Pembelian{}, 0, nil
	}

	// ORDER BY ends in a unique column. Newest first is what an operator wants, but
	// tanggal alone leaves same-day documents in an unspecified order, which lets
	// one appear on two pages while another is never returned.
	// pembelian_tanggal_id_idx from migration 000012 supports exactly this.
	query := `SELECT ` + pembelianReadColumns + pembelianFrom + pembelianFilter + `
		ORDER BY p.tanggal DESC, p.id DESC
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, statusPenerimaan, idSupplier, dari, sampai, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select pembelian: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Pembelian, 0, limit)

	for rows.Next() {
		var pembelian entity.Pembelian

		if err := scanPembelianRead(rows, &pembelian); err != nil {
			return nil, 0, fmt.Errorf("scan pembelian: %w", err)
		}

		list = append(list, pembelian)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pembelian: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
func (r *PembelianRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.PembelianDetail) error {
	const query = `
		INSERT INTO pembelian_detail (
			id_pembelian, id_product, qty_faktur, qty_diterima, id_satuan_input,
			faktor_konversi, qty_dasar, qty_diterima_dasar, harga_satuan_input,
			diskon_baris, subtotal, jumlah_koli, keterangan_selisih
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDPembelian, detail.IDProduct, detail.QtyFaktur, detail.QtyDiterima,
		detail.IDSatuanInput, detail.FaktorKonversi, detail.QtyDasar,
		detail.QtyDiterimaDasar, detail.HargaSatuanInput, detail.DiskonBaris,
		detail.Subtotal, detail.JumlahKoli, detail.KeteranganSelisih,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert pembelian_detail: %w", err)
	}

	return nil
}

// DeleteDetail clears every line of a document, for the wholesale replace that
// PUT /pembelian/{id}/detail performs.
//
// This is one of the few DELETEs in the codebase, and it is only ever reached on a
// DRAFT. A posted document's lines are referenced by retur_pembelian_detail and are
// the source of cost for every reversal, so deleting one would erase the audit
// trail — which is why the usecase checks the status first and why nothing here
// tries to make this safe on its own.
func (r *PembelianRepository) DeleteDetail(ctx context.Context, db DBTX, idPembelian int64) error {
	const query = `DELETE FROM pembelian_detail WHERE id_pembelian = $1`

	if _, err := db.ExecContext(ctx, query, idPembelian); err != nil {
		return fmt.Errorf("delete pembelian_detail: %w", err)
	}

	return nil
}

// FindDetail returns a document's lines in insertion order, so the screen shows
// them the way they were typed off the paper invoice.
func (r *PembelianRepository) FindDetail(ctx context.Context, db DBTX, idPembelian int64) ([]entity.PembelianDetail, error) {
	const query = `SELECT ` + pembelianDetailReadColumns + pembelianDetailFrom + `
		WHERE d.id_pembelian = $1
		ORDER BY d.id`

	rows, err := db.QueryContext(ctx, query, idPembelian)
	if err != nil {
		return nil, fmt.Errorf("select pembelian_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PembelianDetail, 0, 8)

	for rows.Next() {
		var detail entity.PembelianDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDPembelian, &detail.IDProduct, &detail.QtyFaktur,
			&detail.QtyDiterima, &detail.IDSatuanInput, &detail.FaktorKonversi,
			&detail.QtyDasar, &detail.QtyDiterimaDasar, &detail.QtySusulanDasar,
			&detail.HargaSatuanInput,
			&detail.DiskonBaris, &detail.Subtotal, &detail.JumlahKoli,
			&detail.AlokasiBiaya, &detail.HargaPokokSatuanDasar, &detail.KeteranganSelisih,
			&detail.NamaProduct, &detail.KodeBarang, &detail.NamaSatuan, &detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan pembelian_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pembelian_detail: %w", err)
	}

	return list, nil
}

// FindDetailByID returns one line with the product and unit names attached, plus
// what has already been received against it.
//
// This is what a follow-up receipt reads to learn the outstanding quantity and the
// cost to copy. Kept separate from FindDetail so the caller does not have to fetch a
// whole document to reach one line.
func (r *PembelianRepository) FindDetailByID(ctx context.Context, db DBTX, id int64) (*entity.PembelianDetail, error) {
	const query = `SELECT ` + pembelianDetailReadColumns + pembelianDetailFrom + ` WHERE d.id = $1`

	detail := new(entity.PembelianDetail)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&detail.ID, &detail.IDPembelian, &detail.IDProduct, &detail.QtyFaktur,
		&detail.QtyDiterima, &detail.IDSatuanInput, &detail.FaktorKonversi,
		&detail.QtyDasar, &detail.QtyDiterimaDasar, &detail.QtySusulanDasar,
		&detail.HargaSatuanInput,
		&detail.DiskonBaris, &detail.Subtotal, &detail.JumlahKoli,
		&detail.AlokasiBiaya, &detail.HargaPokokSatuanDasar, &detail.KeteranganSelisih,
		&detail.NamaProduct, &detail.KodeBarang, &detail.NamaSatuan, &detail.NamaSatuanDasar,
	)
	if err != nil {
		return nil, err
	}

	return detail, nil
}

// RecalculateStatusPenerimaan rebuilds the receipt cache from the lines and every
// POSTED follow-up receipt against them.
//
// status_penerimaan follows the same rule as status_pembayaran: always recomputed,
// never set from a form. Posting or voiding a penerimaan_susulan is what changes the
// answer, and the purchase itself is POSTED by then and can no longer recompute it
// on its own.
//
// One statement, so there is no window where the cache disagrees with the rows it
// summarises.
func (r *PembelianRepository) RecalculateStatusPenerimaan(ctx context.Context, db DBTX, idPembelian int64) error {
	const query = `
		UPDATE pembelian p SET status_penerimaan = CASE
			WHEN EXISTS (
				SELECT 1 FROM pembelian_detail d
				WHERE d.id_pembelian = p.id
				  AND d.qty_dasar > d.qty_diterima_dasar + ` + pembelianDetailSusulan + `
			) THEN 'KURANG'
			ELSE 'LENGKAP'
		END
		WHERE p.id = $1`

	if _, err := db.ExecContext(ctx, query, idPembelian); err != nil {
		return fmt.Errorf("recalculate status_penerimaan: %w", err)
	}

	return nil
}

// HasPostedSusulan reports whether a follow-up receipt has already been posted
// against a purchase.
//
// Cancelling the purchase reverses only the rows the purchase itself wrote
// (ref_table = 'pembelian'), so a posted follow-up's stock would survive with no
// document left explaining it — and it could not be reversed afterwards either,
// since its own cancellation path needs a purchase that is still POSTED. Void the
// follow-up first.
func (r *PembelianRepository) HasPostedSusulan(ctx context.Context, db DBTX, idPembelian int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM penerimaan_susulan
			WHERE id_pembelian = $1 AND status = 'POSTED'
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, idPembelian).Scan(&exists); err != nil {
		return false, fmt.Errorf("check penerimaan_susulan POSTED: %w", err)
	}

	return exists, nil
}

// UpdateDetailPosting writes back what posting computed for one line.
func (r *PembelianRepository) UpdateDetailPosting(ctx context.Context, db DBTX, id int64, alokasiBiaya, hargaPokokSatuanDasar string) error {
	const query = `
		UPDATE pembelian_detail SET
			alokasi_biaya            = $2::NUMERIC,
			harga_pokok_satuan_dasar = $3::NUMERIC
		WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, alokasiBiaya, hargaPokokSatuanDasar); err != nil {
		return fmt.Errorf("update pembelian_detail posting: %w", err)
	}

	return nil
}

// UpdateJumlahKoli sets one line's share of the shipment, for the "bagi rata"
// button that splits total_koli across the lines.
func (r *PembelianRepository) UpdateJumlahKoli(ctx context.Context, db DBTX, id int64, jumlahKoli string) error {
	const query = `UPDATE pembelian_detail SET jumlah_koli = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, jumlahKoli); err != nil {
		return fmt.Errorf("update jumlah_koli: %w", err)
	}

	return nil
}

// pembelianFields lists the scan targets in the order of pembelianColumns, once, so
// the two read paths cannot drift apart from each other or from the constant.
func pembelianFields(pembelian *entity.Pembelian) []any {
	return []any{
		&pembelian.ID, &pembelian.Nomor, &pembelian.Tanggal, &pembelian.IDSupplier,
		&pembelian.IDRuang, &pembelian.NoFakturSupplier, &pembelian.TanggalFaktur,
		&pembelian.Subtotal, &pembelian.DiskonNota, &pembelian.PPN,
		&pembelian.PPNDikreditkan, &pembelian.Pembulatan, &pembelian.Total,
		&pembelian.IDEkspedisi, &pembelian.NoResi, &pembelian.TotalKoli,
		&pembelian.TarifPerKoli, &pembelian.BiayaAngkut, &pembelian.DitanggungSupplier,
		&pembelian.MetodeAlokasiAngkut, &pembelian.JenisPembayaran,
		&pembelian.StatusPembayaran, &pembelian.StatusPenerimaan, &pembelian.Status,
		&pembelian.CreatedBy, &pembelian.CreatedAt,
		&pembelian.DiajukanOleh, &pembelian.DiajukanPada, &pembelian.DisetujuiOleh,
		&pembelian.DisetujuiPada, &pembelian.PostedAt,
		&pembelian.DibatalkanOleh, &pembelian.AlasanBatal, &pembelian.AlasanTolak,
	}
}

func scanPembelian(row rowScanner, pembelian *entity.Pembelian) error {
	return row.Scan(pembelianFields(pembelian)...)
}

func scanPembelianRead(row rowScanner, pembelian *entity.Pembelian) error {
	return row.Scan(append(
		pembelianFields(pembelian), &pembelian.NamaSupplier, &pembelian.NamaRuang,
	)...)
}
