package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PenerimaanPembayaranRepository owns every statement touching penerimaan_pembayaran
// and pembayaran_alokasi — the mirror of PembayaranUtangRepository, money flowing
// the other way.
type PenerimaanPembayaranRepository struct{}

func NewPenerimaanPembayaranRepository() *PenerimaanPembayaranRepository {
	return &PenerimaanPembayaranRepository{}
}

// penerimaanColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
const penerimaanColumns = `id, nomor, tanggal, id_pelanggan, metode, no_referensi,
	nama_bank, tanggal_jatuh_tempo_giro, tanggal_cair, status_giro,
	jumlah::TEXT, jumlah_dialokasikan::TEXT, status, keterangan,
	created_by, created_at, posted_at, dibatalkan_oleh, alasan_batal`

// penerimaanReadColumns adds the customer name. Fetching it per row would be an N+1.
const penerimaanReadColumns = `p.id, p.nomor, p.tanggal, p.id_pelanggan, p.metode,
	p.no_referensi, p.nama_bank, p.tanggal_jatuh_tempo_giro, p.tanggal_cair, p.status_giro,
	p.jumlah::TEXT, p.jumlah_dialokasikan::TEXT, p.status, p.keterangan,
	p.created_by, p.created_at, p.posted_at, p.dibatalkan_oleh, p.alasan_batal, pel.nama`

// penerimaanFrom joins INNER: id_pelanggan is NOT NULL and carries a foreign key, so
// a payment without a customer cannot exist.
const penerimaanFrom = `
	FROM penerimaan_pembayaran p
	JOIN pelanggan pel ON pel.id = p.id_pelanggan`

// penerimaanFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$7 and pagination follows after it.
const penerimaanFilter = `
	WHERE ($1 = '' OR p.nomor ILIKE '%' || $1 || '%'
	                OR p.no_referensi ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR p.status = $2)
	  AND ($3 = '' OR p.metode = $3)
	  AND ($4 = '' OR p.status_giro = $4)
	  AND ($5 = 0 OR p.id_pelanggan = $5)
	  AND ($6::DATE IS NULL OR p.tanggal >= $6::DATE)
	  AND ($7::DATE IS NULL OR p.tanggal < ($7::DATE + INTERVAL '1 day'))`

// penerimaanAlokasiReadColumns reaches the nota so a payment screen can name what it
// settles, and report what became of each one, without a query per row.
const penerimaanAlokasiReadColumns = `a.id, a.id_penerimaan_pembayaran, a.id_penjualan,
	a.jumlah::TEXT, a.created_at,
	pj.nomor, pj.total::TEXT, pj.status_pembayaran`

const penerimaanAlokasiFrom = `
	FROM pembayaran_alokasi a
	JOIN penjualan pj ON pj.id = a.id_penjualan`

// PenerimaanPembayaranPatch carries a partial header update. Only what a DRAFT may
// change is here.
//
// id_pelanggan is absent: it is whose receivable this payment reduces, and changing
// it would leave every allocation pointing at another customer's notas. metode is
// absent because it decides whether the giro columns may be filled at all, and the
// CHECK constraint that enforces that would have to be satisfied across a patch that
// can touch either half.
type PenerimaanPembayaranPatch struct {
	Tanggal                  *time.Time
	SetNoReferensi           bool
	NoReferensi              *string
	SetNamaBank              bool
	NamaBank                 *string
	SetTanggalJatuhTempoGiro bool
	TanggalJatuhTempoGiro    *time.Time
	Jumlah                   *string
	SetKeterangan            bool
	Keterangan               *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can
// reach the joined customer name.
func (r *PenerimaanPembayaranRepository) Create(ctx context.Context, db DBTX, pembayaran *entity.PenerimaanPembayaran) error {
	const query = `
		INSERT INTO penerimaan_pembayaran (
			nomor, tanggal, id_pelanggan, metode, no_referensi, nama_bank,
			tanggal_jatuh_tempo_giro, status_giro, jumlah, status, keterangan, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::NUMERIC, $10, $11, $12)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		pembayaran.Nomor, pembayaran.Tanggal, pembayaran.IDPelanggan, pembayaran.Metode,
		pembayaran.NoReferensi, pembayaran.NamaBank,
		pembayaran.TanggalJatuhTempoGiro, pembayaran.StatusGiro,
		pembayaran.Jumlah, pembayaran.Status, pembayaran.Keterangan, pembayaran.CreatedBy,
	).Scan(&pembayaran.ID, &pembayaran.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert penerimaan_pembayaran: %w", err)
	}

	return nil
}

func (r *PenerimaanPembayaranRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.PenerimaanPembayaran, error) {
	const query = `SELECT ` + penerimaanReadColumns + penerimaanFrom + ` WHERE p.id = $1`

	pembayaran := new(entity.PenerimaanPembayaran)

	if err := scanPenerimaanRead(db.QueryRowContext(ctx, query, id), pembayaran); err != nil {
		return nil, err
	}

	return pembayaran, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *PenerimaanPembayaranRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.PenerimaanPembayaran, error) {
	const query = `SELECT ` + penerimaanColumns + ` FROM penerimaan_pembayaran WHERE id = $1 FOR UPDATE`

	pembayaran := new(entity.PenerimaanPembayaran)

	if err := scanPenerimaan(db.QueryRowContext(ctx, query, id), pembayaran); err != nil {
		return nil, err
	}

	return pembayaran, nil
}

func (r *PenerimaanPembayaranRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch PenerimaanPembayaranPatch) error {
	const query = `
		UPDATE penerimaan_pembayaran SET
			tanggal                  = COALESCE($2, tanggal),
			no_referensi             = CASE WHEN $3::BOOLEAN THEN $4 ELSE no_referensi END,
			nama_bank                = CASE WHEN $5::BOOLEAN THEN $6 ELSE nama_bank END,
			tanggal_jatuh_tempo_giro = CASE WHEN $7::BOOLEAN THEN $8 ELSE tanggal_jatuh_tempo_giro END,
			jumlah                   = COALESCE($9::NUMERIC, jumlah),
			keterangan               = CASE WHEN $10::BOOLEAN THEN $11 ELSE keterangan END
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Tanggal,
		patch.SetNoReferensi, patch.NoReferensi,
		patch.SetNamaBank, patch.NamaBank,
		patch.SetTanggalJatuhTempoGiro, patch.TanggalJatuhTempoGiro,
		patch.Jumlah,
		patch.SetKeterangan, patch.Keterangan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// RecalculateDialokasikan rewrites the header's allocated total from its own rows.
//
// One statement, so there is no window where the cache disagrees with what it
// summarises. penerimaan_pembayaran_alokasi_check (jumlah_dialokasikan <= jumlah) is
// what catches an over-allocation the usecase somehow let through: it arrives as
// 23514.
func (r *PenerimaanPembayaranRepository) RecalculateDialokasikan(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE penerimaan_pembayaran p SET jumlah_dialokasikan = COALESCE((
			SELECT SUM(a.jumlah) FROM pembayaran_alokasi a
			WHERE a.id_penerimaan_pembayaran = p.id
		), 0)
		WHERE p.id = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("recalculate jumlah_dialokasikan: %w", err)
	}

	return nil
}

// Posting closes the document. Called in the same transaction as the receivable
// caches it moves, so a failure at either end leaves neither.
func (r *PenerimaanPembayaranRepository) Posting(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE penerimaan_pembayaran SET
			status    = 'POSTED',
			posted_at = now()
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "posting penerimaan_pembayaran", id)
}

func (r *PenerimaanPembayaranRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE penerimaan_pembayaran SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal penerimaan_pembayaran", id, actorID, alasan)
}

// CairkanGiro records that a customer's giro actually cleared the bank.
//
// This is the moment the receivable drops — not when the paper was handed over. The
// guard insists on BELUM_CAIR so a cleared giro cannot be cleared twice and a
// bounced one cannot be quietly revived.
func (r *PenerimaanPembayaranRepository) CairkanGiro(ctx context.Context, db DBTX, id int64, tanggalCair time.Time) error {
	const query = `
		UPDATE penerimaan_pembayaran SET
			status_giro  = 'CAIR',
			tanggal_cair = $2
		WHERE id = $1 AND status = 'POSTED' AND metode = 'GIRO' AND status_giro = 'BELUM_CAIR'`

	return execTransisi(ctx, db, query, "cairkan giro", id, tanggalCair)
}

// TolakGiro records that a customer's giro bounced. Nothing has to be given back:
// it never reduced a receivable, so there is nothing to reverse — only the status
// changes.
func (r *PenerimaanPembayaranRepository) TolakGiro(ctx context.Context, db DBTX, id int64, keterangan *string) error {
	const query = `
		UPDATE penerimaan_pembayaran SET
			status_giro = 'TOLAK',
			keterangan  = COALESCE($2, keterangan)
		WHERE id = $1 AND status = 'POSTED' AND metode = 'GIRO' AND status_giro = 'BELUM_CAIR'`

	return execTransisi(ctx, db, query, "tolak giro", id, keterangan)
}

func (r *PenerimaanPembayaranRepository) Search(ctx context.Context, db DBTX, search, status, metode, statusGiro string, idPelanggan int64, dari, sampai *string, limit, offset int) ([]entity.PenerimaanPembayaran, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM penerimaan_pembayaran p`+penerimaanFilter,
		search, status, metode, statusGiro, idPelanggan, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count penerimaan_pembayaran: %w", err)
	}

	if total == 0 {
		return []entity.PenerimaanPembayaran{}, 0, nil
	}

	// ORDER BY ends in a unique column, so a page boundary between same-day
	// payments cannot repeat or skip one. penerimaan_pembayaran_tanggal_id_idx
	// from migration 000024 supports exactly this.
	query := `SELECT ` + penerimaanReadColumns + penerimaanFrom + penerimaanFilter + `
		ORDER BY p.tanggal DESC, p.id DESC
		LIMIT $8 OFFSET $9`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, metode, statusGiro, idPelanggan, dari, sampai, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select penerimaan_pembayaran: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PenerimaanPembayaran, 0, limit)

	for rows.Next() {
		var pembayaran entity.PenerimaanPembayaran

		if err := scanPenerimaanRead(rows, &pembayaran); err != nil {
			return nil, 0, fmt.Errorf("scan penerimaan_pembayaran: %w", err)
		}

		list = append(list, pembayaran)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate penerimaan_pembayaran: %w", err)
	}

	return list, total, nil
}

// SumAlokasiEfektifKecuali totals what has effectively been received against a nota
// by every payment except one.
//
// The exclusion is what makes it usable from inside a payment's own posting or
// giro-clearing path: it answers "how much of this nota is already committed by
// everybody else", which is the balance the excluded document's own share has to
// fit inside. Without it, clearing a giro whose allocations already count would
// measure them against a balance they themselves consumed, and a correct payment
// would be refused.
//
// "Effective" carries the giro rule: a POSTED payment counts unless it is a giro
// that has not cleared. It mirrors penjualanAlokasiEfektif in
// penjualan_repository.go, and the two have to agree — one decides whether an
// allocation is allowed, the other decides what status_pembayaran becomes because
// of it.
func (r *PenerimaanPembayaranRepository) SumAlokasiEfektifKecuali(ctx context.Context, db DBTX, idPenjualan, kecualiID int64) (string, error) {
	const query = `
		SELECT COALESCE(SUM(a.jumlah), 0)::NUMERIC(20, 2)::TEXT
		FROM pembayaran_alokasi a
		JOIN penerimaan_pembayaran pp ON pp.id = a.id_penerimaan_pembayaran
		WHERE a.id_penjualan = $1
		  AND pp.id <> $2
		  AND pp.status = 'POSTED'
		  AND (pp.metode <> 'GIRO' OR pp.status_giro = 'CAIR')`

	var jumlah string
	if err := db.QueryRowContext(ctx, query, idPenjualan, kecualiID).Scan(&jumlah); err != nil {
		return "", fmt.Errorf("sum alokasi efektif: %w", err)
	}

	return jumlah, nil
}

// InsertAlokasi writes one allocation.
//
// A repeat of the same nota inside one payment raises 23505 from
// pembayaran_alokasi_baris_uidx. That index matters: without it two rows for the
// same nota would each pass the remaining-balance check on their own and together
// exceed it.
func (r *PenerimaanPembayaranRepository) InsertAlokasi(ctx context.Context, db DBTX, alokasi *entity.PembayaranAlokasi) error {
	const query = `
		INSERT INTO pembayaran_alokasi (id_penerimaan_pembayaran, id_penjualan, jumlah)
		VALUES ($1, $2, $3::NUMERIC)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		alokasi.IDPenerimaanPembayaran, alokasi.IDPenjualan, alokasi.Jumlah,
	).Scan(&alokasi.ID, &alokasi.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pembayaran_alokasi: %w", err)
	}

	return nil
}

// DeleteAlokasi clears every allocation, for the wholesale replace that
// PUT /penerimaan-pembayaran/{id}/alokasi performs. Only ever reached on a DRAFT: a
// posted allocation is what a nota's status_pembayaran is computed from.
func (r *PenerimaanPembayaranRepository) DeleteAlokasi(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM pembayaran_alokasi WHERE id_penerimaan_pembayaran = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete pembayaran_alokasi: %w", err)
	}

	return nil
}

func (r *PenerimaanPembayaranRepository) FindAlokasi(ctx context.Context, db DBTX, id int64) ([]entity.PembayaranAlokasi, error) {
	const query = `SELECT ` + penerimaanAlokasiReadColumns + penerimaanAlokasiFrom + `
		WHERE a.id_penerimaan_pembayaran = $1
		ORDER BY a.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select pembayaran_alokasi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PembayaranAlokasi, 0, 8)

	for rows.Next() {
		var alokasi entity.PembayaranAlokasi

		if err := rows.Scan(
			&alokasi.ID, &alokasi.IDPenerimaanPembayaran, &alokasi.IDPenjualan,
			&alokasi.Jumlah, &alokasi.CreatedAt,
			&alokasi.NomorPenjualan, &alokasi.Total, &alokasi.StatusPembayaran,
		); err != nil {
			return nil, fmt.Errorf("scan pembayaran_alokasi: %w", err)
		}

		list = append(list, alokasi)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pembayaran_alokasi: %w", err)
	}

	return list, nil
}

// FindIDPenjualanAlokasi returns just the nota ids a payment points at, in
// ascending order.
//
// Posting, voiding, and clearing a giro all have to recompute status_pembayaran on
// every nota this payment touches, and none of them need the joined names to do it.
// Ascending order is not cosmetic: it fixes a single lock order across those three
// paths, so two of them running against overlapping nota sets queue instead of
// deadlocking.
func (r *PenerimaanPembayaranRepository) FindIDPenjualanAlokasi(ctx context.Context, db DBTX, id int64) ([]int64, error) {
	const query = `
		SELECT id_penjualan FROM pembayaran_alokasi
		WHERE id_penerimaan_pembayaran = $1
		ORDER BY id_penjualan`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select id_penjualan alokasi: %w", err)
	}
	defer rows.Close()

	list := make([]int64, 0, 8)

	for rows.Next() {
		var idPenjualan int64
		if err := rows.Scan(&idPenjualan); err != nil {
			return nil, fmt.Errorf("scan id_penjualan alokasi: %w", err)
		}

		list = append(list, idPenjualan)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate id_penjualan alokasi: %w", err)
	}

	return list, nil
}

// penerimaanFields lists the scan targets in the order of penerimaanColumns, once,
// so the two read paths cannot drift apart from each other or from the constant.
func penerimaanFields(pembayaran *entity.PenerimaanPembayaran) []any {
	return []any{
		&pembayaran.ID, &pembayaran.Nomor, &pembayaran.Tanggal, &pembayaran.IDPelanggan,
		&pembayaran.Metode, &pembayaran.NoReferensi, &pembayaran.NamaBank,
		&pembayaran.TanggalJatuhTempoGiro, &pembayaran.TanggalCair, &pembayaran.StatusGiro,
		&pembayaran.Jumlah, &pembayaran.JumlahDialokasikan, &pembayaran.Status,
		&pembayaran.Keterangan,
		&pembayaran.CreatedBy, &pembayaran.CreatedAt, &pembayaran.PostedAt,
		&pembayaran.DibatalkanOleh, &pembayaran.AlasanBatal,
	}
}

func scanPenerimaan(row rowScanner, pembayaran *entity.PenerimaanPembayaran) error {
	return row.Scan(penerimaanFields(pembayaran)...)
}

func scanPenerimaanRead(row rowScanner, pembayaran *entity.PenerimaanPembayaran) error {
	return row.Scan(append(penerimaanFields(pembayaran), &pembayaran.NamaPelanggan)...)
}
