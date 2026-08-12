package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PembayaranUtangRepository owns every statement touching pembayaran_utang and
// pembayaran_utang_alokasi.
type PembayaranUtangRepository struct{}

func NewPembayaranUtangRepository() *PembayaranUtangRepository {
	return &PembayaranUtangRepository{}
}

// pembayaranColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
const pembayaranColumns = `id, nomor, tanggal, id_supplier, metode, no_referensi,
	nama_bank, tanggal_jatuh_tempo_giro, tanggal_cair, status_giro,
	jumlah::TEXT, jumlah_dialokasikan::TEXT, status, keterangan,
	created_by, created_at, posted_at, dibatalkan_oleh, alasan_batal`

// pembayaranReadColumns adds the supplier name. Fetching it per row would be an N+1.
const pembayaranReadColumns = `p.id, p.nomor, p.tanggal, p.id_supplier, p.metode,
	p.no_referensi, p.nama_bank, p.tanggal_jatuh_tempo_giro, p.tanggal_cair, p.status_giro,
	p.jumlah::TEXT, p.jumlah_dialokasikan::TEXT, p.status, p.keterangan,
	p.created_by, p.created_at, p.posted_at, p.dibatalkan_oleh, p.alasan_batal, s.nama`

// pembayaranFrom joins INNER: id_supplier is NOT NULL and carries a foreign key, so a
// payment without a supplier cannot exist.
const pembayaranFrom = `
	FROM pembayaran_utang p
	JOIN supplier s ON s.id = p.id_supplier`

// pembayaranFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$7 and pagination follows after it.
const pembayaranFilter = `
	WHERE ($1 = '' OR p.nomor ILIKE '%' || $1 || '%'
	                OR p.no_referensi ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR p.status = $2)
	  AND ($3 = '' OR p.metode = $3)
	  AND ($4 = '' OR p.status_giro = $4)
	  AND ($5 = 0 OR p.id_supplier = $5)
	  AND ($6::DATE IS NULL OR p.tanggal >= $6::DATE)
	  AND ($7::DATE IS NULL OR p.tanggal < ($7::DATE + INTERVAL '1 day'))`

// pembayaranAlokasiReadColumns reaches the invoice so a payment screen can name what it
// settles, and report what became of each one, without a query per row.
const pembayaranAlokasiReadColumns = `a.id, a.id_pembayaran_utang, a.id_pembelian,
	a.jumlah::TEXT, a.created_at,
	pb.nomor, pb.no_faktur_supplier, pb.total::TEXT, pb.status_pembayaran`

const pembayaranAlokasiFrom = `
	FROM pembayaran_utang_alokasi a
	JOIN pembelian pb ON pb.id = a.id_pembelian`

// PembayaranUtangPatch carries a partial header update. Only what a DRAFT may change is
// here.
//
// id_supplier is absent: it is who the money goes to, and changing it would leave every
// allocation pointing at another supplier's invoices. metode is absent because it decides
// whether the giro columns may be filled at all, and the CHECK constraint that enforces
// that would have to be satisfied across a patch that can touch either half.
type PembayaranUtangPatch struct {
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

// Create inserts the header and fills ID. It returns only the generated key; the response
// is always re-read through FindByID, which is the only query that can reach the joined
// supplier name.
func (r *PembayaranUtangRepository) Create(ctx context.Context, db DBTX, pembayaran *entity.PembayaranUtang) error {
	const query = `
		INSERT INTO pembayaran_utang (
			nomor, tanggal, id_supplier, metode, no_referensi, nama_bank,
			tanggal_jatuh_tempo_giro, status_giro, jumlah, status, keterangan, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::NUMERIC, $10, $11, $12)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		pembayaran.Nomor, pembayaran.Tanggal, pembayaran.IDSupplier, pembayaran.Metode,
		pembayaran.NoReferensi, pembayaran.NamaBank,
		pembayaran.TanggalJatuhTempoGiro, pembayaran.StatusGiro,
		pembayaran.Jumlah, pembayaran.Status, pembayaran.Keterangan, pembayaran.CreatedBy,
	).Scan(&pembayaran.ID, &pembayaran.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pembayaran_utang: %w", err)
	}

	return nil
}

func (r *PembayaranUtangRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.PembayaranUtang, error) {
	const query = `SELECT ` + pembayaranReadColumns + pembayaranFrom + ` WHERE p.id = $1`

	pembayaran := new(entity.PembayaranUtang)

	if err := scanPembayaranRead(db.QueryRowContext(ctx, query, id), pembayaran); err != nil {
		return nil, err
	}

	return pembayaran, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a state
// transition cannot race another one. See PembelianRepository.LockByID.
func (r *PembayaranUtangRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.PembayaranUtang, error) {
	const query = `SELECT ` + pembayaranColumns + ` FROM pembayaran_utang WHERE id = $1 FOR UPDATE`

	pembayaran := new(entity.PembayaranUtang)

	if err := scanPembayaran(db.QueryRowContext(ctx, query, id), pembayaran); err != nil {
		return nil, err
	}

	return pembayaran, nil
}

func (r *PembayaranUtangRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch PembayaranUtangPatch) error {
	const query = `
		UPDATE pembayaran_utang SET
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
// One statement, so there is no window where the cache disagrees with what it summarises.
// pembayaran_utang_alokasi_check (jumlah_dialokasikan <= jumlah) is what catches an
// over-allocation the usecase somehow let through: it arrives as 23514.
func (r *PembayaranUtangRepository) RecalculateDialokasikan(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE pembayaran_utang p SET jumlah_dialokasikan = COALESCE((
			SELECT SUM(a.jumlah) FROM pembayaran_utang_alokasi a
			WHERE a.id_pembayaran_utang = p.id
		), 0)
		WHERE p.id = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("recalculate jumlah_dialokasikan: %w", err)
	}

	return nil
}

// Posting closes the document. Called in the same transaction as the payable caches it
// moves, so a failure at either end leaves neither.
func (r *PembayaranUtangRepository) Posting(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE pembayaran_utang SET
			status    = 'POSTED',
			posted_at = now()
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "posting pembayaran_utang", id)
}

func (r *PembayaranUtangRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE pembayaran_utang SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal pembayaran_utang", id, actorID, alasan)
}

// CairkanGiro records that a giro actually cleared the bank.
//
// This is the moment the payable drops — not when the paper was handed over. The guard
// insists on BELUM_CAIR so a cleared giro cannot be cleared twice and a bounced one
// cannot be quietly revived.
func (r *PembayaranUtangRepository) CairkanGiro(ctx context.Context, db DBTX, id int64, tanggalCair time.Time) error {
	const query = `
		UPDATE pembayaran_utang SET
			status_giro  = 'CAIR',
			tanggal_cair = $2
		WHERE id = $1 AND status = 'POSTED' AND metode = 'GIRO' AND status_giro = 'BELUM_CAIR'`

	return execTransisi(ctx, db, query, "cairkan giro", id, tanggalCair)
}

// TolakGiro records that a giro bounced. Nothing has to be given back: it never reduced
// a payable, so there is nothing to reverse — only the status changes.
func (r *PembayaranUtangRepository) TolakGiro(ctx context.Context, db DBTX, id int64, keterangan *string) error {
	const query = `
		UPDATE pembayaran_utang SET
			status_giro = 'TOLAK',
			keterangan  = COALESCE($2, keterangan)
		WHERE id = $1 AND status = 'POSTED' AND metode = 'GIRO' AND status_giro = 'BELUM_CAIR'`

	return execTransisi(ctx, db, query, "tolak giro", id, keterangan)
}

func (r *PembayaranUtangRepository) Search(ctx context.Context, db DBTX, search, status, metode, statusGiro string, idSupplier int64, dari, sampai *string, limit, offset int) ([]entity.PembayaranUtang, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM pembayaran_utang p`+pembayaranFilter,
		search, status, metode, statusGiro, idSupplier, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count pembayaran_utang: %w", err)
	}

	if total == 0 {
		return []entity.PembayaranUtang{}, 0, nil
	}

	// ORDER BY ends in a unique column, so a page boundary between same-day payments
	// cannot repeat or skip one. pembayaran_utang_tanggal_id_idx from migration 000015
	// supports exactly this.
	query := `SELECT ` + pembayaranReadColumns + pembayaranFrom + pembayaranFilter + `
		ORDER BY p.tanggal DESC, p.id DESC
		LIMIT $8 OFFSET $9`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, metode, statusGiro, idSupplier, dari, sampai, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select pembayaran_utang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PembayaranUtang, 0, limit)

	for rows.Next() {
		var pembayaran entity.PembayaranUtang

		if err := scanPembayaranRead(rows, &pembayaran); err != nil {
			return nil, 0, fmt.Errorf("scan pembayaran_utang: %w", err)
		}

		list = append(list, pembayaran)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pembayaran_utang: %w", err)
	}

	return list, total, nil
}

// SumAlokasiEfektifKecuali totals what has effectively been paid against an invoice by
// every payment except one.
//
// The exclusion is what makes it usable from inside a payment's own posting or giro-clearing
// path: it answers "how much of this invoice is already committed by everybody else", which
// is the balance the excluded document's own share has to fit inside. Without it, clearing a
// giro whose allocations already count would measure them against a balance they themselves
// consumed, and a correct payment would be refused.
//
// "Effective" carries the giro rule: a POSTED payment counts unless it is a giro that has
// not cleared. It mirrors pembelianAlokasiEfektif in pembelian_repository.go, and the two
// have to agree — one decides whether an allocation is allowed, the other decides what
// status_pembayaran becomes because of it.
func (r *PembayaranUtangRepository) SumAlokasiEfektifKecuali(ctx context.Context, db DBTX, idPembelian, kecualiID int64) (string, error) {
	const query = `
		SELECT COALESCE(SUM(a.jumlah), 0)::NUMERIC(20, 2)::TEXT
		FROM pembayaran_utang_alokasi a
		JOIN pembayaran_utang pu ON pu.id = a.id_pembayaran_utang
		WHERE a.id_pembelian = $1
		  AND pu.id <> $2
		  AND pu.status = 'POSTED'
		  AND (pu.metode <> 'GIRO' OR pu.status_giro = 'CAIR')`

	var jumlah string
	if err := db.QueryRowContext(ctx, query, idPembelian, kecualiID).Scan(&jumlah); err != nil {
		return "", fmt.Errorf("sum alokasi efektif: %w", err)
	}

	return jumlah, nil
}

// InsertAlokasi writes one allocation.
//
// A repeat of the same invoice inside one payment raises 23505 from
// pembayaran_utang_alokasi_baris_uidx. That index matters: without it two rows for the
// same invoice would each pass the remaining-balance check on their own and together
// exceed it.
func (r *PembayaranUtangRepository) InsertAlokasi(ctx context.Context, db DBTX, alokasi *entity.PembayaranUtangAlokasi) error {
	const query = `
		INSERT INTO pembayaran_utang_alokasi (id_pembayaran_utang, id_pembelian, jumlah)
		VALUES ($1, $2, $3::NUMERIC)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		alokasi.IDPembayaranUtang, alokasi.IDPembelian, alokasi.Jumlah,
	).Scan(&alokasi.ID, &alokasi.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert pembayaran_utang_alokasi: %w", err)
	}

	return nil
}

// DeleteAlokasi clears every allocation, for the wholesale replace that
// PUT /pembayaran-utang/{id}/alokasi performs. Only ever reached on a DRAFT: a posted
// allocation is what a purchase's status_pembayaran is computed from.
func (r *PembayaranUtangRepository) DeleteAlokasi(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM pembayaran_utang_alokasi WHERE id_pembayaran_utang = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete pembayaran_utang_alokasi: %w", err)
	}

	return nil
}

func (r *PembayaranUtangRepository) FindAlokasi(ctx context.Context, db DBTX, id int64) ([]entity.PembayaranUtangAlokasi, error) {
	const query = `SELECT ` + pembayaranAlokasiReadColumns + pembayaranAlokasiFrom + `
		WHERE a.id_pembayaran_utang = $1
		ORDER BY a.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select pembayaran_utang_alokasi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PembayaranUtangAlokasi, 0, 8)

	for rows.Next() {
		var alokasi entity.PembayaranUtangAlokasi

		if err := rows.Scan(
			&alokasi.ID, &alokasi.IDPembayaranUtang, &alokasi.IDPembelian,
			&alokasi.Jumlah, &alokasi.CreatedAt,
			&alokasi.NomorPembelian, &alokasi.NoFakturSupplier,
			&alokasi.TotalPembelian, &alokasi.StatusPembayaran,
		); err != nil {
			return nil, fmt.Errorf("scan pembayaran_utang_alokasi: %w", err)
		}

		list = append(list, alokasi)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pembayaran_utang_alokasi: %w", err)
	}

	return list, nil
}

// FindIDPembelianAlokasi returns just the invoice ids a payment points at, in ascending
// order.
//
// Posting, voiding, and clearing a giro all have to recompute status_pembayaran on every
// invoice this payment touches, and none of them need the joined names to do it.
// Ascending order is not cosmetic: it fixes a single lock order across those three paths,
// so two of them running against overlapping invoice sets queue instead of deadlocking.
func (r *PembayaranUtangRepository) FindIDPembelianAlokasi(ctx context.Context, db DBTX, id int64) ([]int64, error) {
	const query = `
		SELECT id_pembelian FROM pembayaran_utang_alokasi
		WHERE id_pembayaran_utang = $1
		ORDER BY id_pembelian`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select id_pembelian alokasi: %w", err)
	}
	defer rows.Close()

	list := make([]int64, 0, 8)

	for rows.Next() {
		var idPembelian int64
		if err := rows.Scan(&idPembelian); err != nil {
			return nil, fmt.Errorf("scan id_pembelian alokasi: %w", err)
		}

		list = append(list, idPembelian)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate id_pembelian alokasi: %w", err)
	}

	return list, nil
}

// pembayaranFields lists the scan targets in the order of pembayaranColumns, once, so the
// two read paths cannot drift apart from each other or from the constant.
func pembayaranFields(pembayaran *entity.PembayaranUtang) []any {
	return []any{
		&pembayaran.ID, &pembayaran.Nomor, &pembayaran.Tanggal, &pembayaran.IDSupplier,
		&pembayaran.Metode, &pembayaran.NoReferensi, &pembayaran.NamaBank,
		&pembayaran.TanggalJatuhTempoGiro, &pembayaran.TanggalCair, &pembayaran.StatusGiro,
		&pembayaran.Jumlah, &pembayaran.JumlahDialokasikan, &pembayaran.Status,
		&pembayaran.Keterangan,
		&pembayaran.CreatedBy, &pembayaran.CreatedAt, &pembayaran.PostedAt,
		&pembayaran.DibatalkanOleh, &pembayaran.AlasanBatal,
	}
}

func scanPembayaran(row rowScanner, pembayaran *entity.PembayaranUtang) error {
	return row.Scan(pembayaranFields(pembayaran)...)
}

func scanPembayaranRead(row rowScanner, pembayaran *entity.PembayaranUtang) error {
	return row.Scan(append(pembayaranFields(pembayaran), &pembayaran.NamaSupplier)...)
}
