package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PenjualanRepository owns every statement touching penjualan and penjualan_detail,
// plus the piutang-pelanggan projection (isu #10 fase 2) that reads the same table.
type PenjualanRepository struct{}

func NewPenjualanRepository() *PenjualanRepository {
	return &PenjualanRepository{}
}

// penjualanColumns is the unqualified list, for INSERT ... RETURNING and the
// SELECT ... FOR UPDATE that cannot reach the joined tables.
//
// Every NUMERIC is cast to TEXT. Scanning NUMERIC into a float64 rounds money, and
// these figures become a nota total and, for a KREDIT sale, a receivable.
const penjualanColumns = `id, nomor, tanggal, id_ruang, id_pelanggan,
	subtotal::TEXT, diskon_nota::TEXT, pembulatan::TEXT, total::TEXT, total_hpp::TEXT,
	jenis_pembayaran, status_pembayaran, status, created_by, created_at, posted_at,
	dibatalkan_oleh, alasan_batal`

// penjualanReadColumns adds the room's name and the customer's, resolved by the
// joins in penjualanFrom. Fetching either per row would be an N+1.
const penjualanReadColumns = `p.id, p.nomor, p.tanggal, p.id_ruang, p.id_pelanggan,
	p.subtotal::TEXT, p.diskon_nota::TEXT, p.pembulatan::TEXT, p.total::TEXT, p.total_hpp::TEXT,
	p.jenis_pembayaran, p.status_pembayaran, p.status, p.created_by, p.created_at, p.posted_at,
	p.dibatalkan_oleh, p.alasan_batal, r.nama_ruang, pel.nama`

// penjualanFrom joins id_ruang INNER — that column is NOT NULL, so a nota without a
// room cannot exist — and id_pelanggan LEFT: a cash sale legitimately has none.
const penjualanFrom = `
	FROM penjualan p
	JOIN ruang r ON r.id = p.id_ruang
	LEFT JOIN pelanggan pel ON pel.id = p.id_pelanggan`

// penjualanFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$8 and pagination follows after it.
const penjualanFilter = `
	WHERE ($1 = '' OR p.nomor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR p.status = $2)
	  AND ($3 = '' OR p.status_pembayaran = $3)
	  AND ($4 = '' OR p.jenis_pembayaran = $4)
	  AND ($5 = 0 OR p.id_ruang = $5)
	  AND ($6 = 0 OR p.id_pelanggan = $6)
	  AND ($7::DATE IS NULL OR p.tanggal >= $7::DATE)
	  AND ($8::DATE IS NULL OR p.tanggal < ($8::DATE + INTERVAL '1 day'))`

const penjualanDetailReadColumns = `pd.id, pd.id_penjualan, pd.id_product, pd.qty_input::TEXT,
	pd.id_satuan_input, pd.faktor_konversi, pd.qty_dasar, pd.id_harga_jual,
	pd.harga_satuan_input::TEXT, pd.diskon_baris::TEXT, pd.subtotal::TEXT,
	pd.hpp_satuan_dasar::TEXT, pd.hpp_total::TEXT,
	pr.kode_barang, pr.nama, si.nama, sdasar.nama`

const penjualanDetailFrom = `
	FROM penjualan_detail pd
	JOIN product pr ON pr.id = pd.id_product
	JOIN satuan si ON si.id = pd.id_satuan_input
	JOIN satuan sdasar ON sdasar.id = pr.id_satuan_dasar`

// PenjualanPatch carries a partial header update.
//
// id_pelanggan is the only nullable column among the five, so it is the only one
// needing the CASE WHEN $n::BOOLEAN dance; the rest are NOT NULL and take a plain
// COALESCE, with the usecase rejecting an explicit null before this ever runs — the
// same split UpdatePembelianRequest follows.
type PenjualanPatch struct {
	Tanggal         *time.Time
	IDRuang         *int64
	SetIDPelanggan  bool
	IDPelanggan     *int64
	JenisPembayaran *string
	DiskonNota      *string
	Pembulatan      *string
}

// Create inserts the header and fills ID. It returns only the generated key; the
// response is always re-read through FindByID, which is the only query that can
// reach the joined names.
func (r *PenjualanRepository) Create(ctx context.Context, db DBTX, penjualan *entity.Penjualan) error {
	const query = `
		INSERT INTO penjualan (
			nomor, tanggal, id_ruang, id_pelanggan, jenis_pembayaran, status,
			status_pembayaran, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`

	err := db.QueryRowContext(
		ctx, query,
		penjualan.Nomor, penjualan.Tanggal, penjualan.IDRuang, penjualan.IDPelanggan,
		penjualan.JenisPembayaran, penjualan.Status, penjualan.StatusPembayaran,
		penjualan.CreatedBy,
	).Scan(&penjualan.ID, &penjualan.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert penjualan: %w", err)
	}

	return nil
}

func (r *PenjualanRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Penjualan, error) {
	const query = `SELECT ` + penjualanReadColumns + penjualanFrom + ` WHERE p.id = $1`

	penjualan := new(entity.Penjualan)

	if err := scanPenjualanRead(db.QueryRowContext(ctx, query, id), penjualan); err != nil {
		return nil, err
	}

	return penjualan, nil
}

// LockByID reads the header and holds a row lock until the transaction ends, so a
// state transition cannot race another one. See PembelianRepository.LockByID.
func (r *PenjualanRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.Penjualan, error) {
	const query = `SELECT ` + penjualanColumns + ` FROM penjualan WHERE id = $1 FOR UPDATE`

	penjualan := new(entity.Penjualan)

	if err := scanPenjualan(db.QueryRowContext(ctx, query, id), penjualan); err != nil {
		return nil, err
	}

	return penjualan, nil
}

// UpdateHeader patches a DRAFT. The caller is expected to have taken the row lock
// and checked the status already (kunciDenganStatus), so this carries no status
// guard of its own.
//
// penjualan_kredit_pelanggan_check is the backstop if the caller's own pre-check
// (see PenjualanUseCase.Update) somehow disagrees with what lands here — it arrives
// as SQLSTATE 23514, mapped by invalidOnCheck.
func (r *PenjualanRepository) UpdateHeader(ctx context.Context, db DBTX, id int64, patch PenjualanPatch) error {
	const query = `
		UPDATE penjualan SET
			tanggal          = COALESCE($2, tanggal),
			id_ruang         = COALESCE($3, id_ruang),
			id_pelanggan     = CASE WHEN $4::BOOLEAN THEN $5 ELSE id_pelanggan END,
			jenis_pembayaran = COALESCE($6, jenis_pembayaran),
			diskon_nota      = COALESCE($7::NUMERIC, diskon_nota),
			pembulatan       = COALESCE($8::NUMERIC, pembulatan)
		WHERE id = $1
		RETURNING id`

	var updated int64

	err := db.QueryRowContext(
		ctx, query, id, patch.Tanggal, patch.IDRuang,
		patch.SetIDPelanggan, patch.IDPelanggan, patch.JenisPembayaran,
		patch.DiskonNota, patch.Pembulatan,
	).Scan(&updated)
	if err != nil {
		return err
	}

	return nil
}

// SimpanTotal writes subtotal and total together, always as a pair.
//
// Both figures are computed in Go with math/big.Rat, never in SQL: subtotal is the
// straight sum of every line's own subtotal, and total = subtotal - diskon_nota +
// pembulatan, validated by the usecase before this is ever called. Called from
// Create and ReplaceDetail (subtotal freshly summed from the lines just written)
// and from Update (subtotal unchanged, only diskon_nota or pembulatan moved) — one
// statement rather than three, so there is never a window where subtotal and total
// disagree with each other.
func (r *PenjualanRepository) SimpanTotal(ctx context.Context, db DBTX, id int64, subtotal, total string) error {
	const query = `
		UPDATE penjualan SET subtotal = $2::NUMERIC, total = $3::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, subtotal, total); err != nil {
		return fmt.Errorf("update penjualan total: %w", err)
	}

	return nil
}

// RecalculateTotalHPP writes the header's total_hpp, summed by the usecase from
// every line's hpp_total after posting — the same shape
// PemakaianRepository.RecalculateTotalHPP follows, for the identical reason: it
// exists only once, right after the loop that writes kartu_stok, since nothing
// about a POSTED document's lines can change afterwards.
func (r *PenjualanRepository) RecalculateTotalHPP(ctx context.Context, db DBTX, id int64, totalHPP string) error {
	const query = `UPDATE penjualan SET total_hpp = $2::NUMERIC WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, totalHPP); err != nil {
		return fmt.Errorf("update penjualan total_hpp: %w", err)
	}

	return nil
}

// RecalculateStatusPembayaran rebuilds the payment cache — the receivable-side
// counterpart of PembelianRepository.RecalculateStatusPembayaran, and deliberately
// simpler for now.
//
// A TUNAI nota that is POSTED is LUNAS by construction: the money changed hands at
// the counter and no separate allocation document exists to record that — see the
// "uncashed giro" trap this module does NOT have, in CLAUDE.md. A KREDIT nota has no
// allocation or return to sum yet, since penerimaan_pembayaran and retur_penjualan
// are both out of scope for isu #10, so it stays BELUM until either is built —
// exactly the answer a freshly POSTED pembelian would give if pembayaran_utang did
// not exist. Only Posting calls this; nothing else can move the answer yet.
func (r *PenjualanRepository) RecalculateStatusPembayaran(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE penjualan SET status_pembayaran = CASE
			WHEN jenis_pembayaran = 'TUNAI' THEN 'LUNAS'
			ELSE 'BELUM'
		END
		WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("recalculate penjualan status_pembayaran: %w", err)
	}

	return nil
}

// Posting closes the document. Called after every kartu_stok row is written, in the
// same transaction, so a failure at either end leaves neither.
func (r *PenjualanRepository) Posting(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE penjualan SET
			posted_at = now(),
			status    = 'POSTED'
		WHERE id = $1 AND status = 'DRAFT'`

	return execTransisi(ctx, db, query, "posting penjualan", id)
}

// Batal marks a posted document void. The reversing kartu_stok rows are written by
// the usecase in the same transaction; nothing is deleted anywhere.
func (r *PenjualanRepository) Batal(ctx context.Context, db DBTX, id, actorID int64, alasan string) error {
	const query = `
		UPDATE penjualan SET
			status          = 'BATAL',
			dibatalkan_oleh = $2,
			alasan_batal    = $3
		WHERE id = $1 AND status = 'POSTED'`

	return execTransisi(ctx, db, query, "batal penjualan", id, actorID, alasan)
}

// Search returns one page plus the total matching count.
func (r *PenjualanRepository) Search(ctx context.Context, db DBTX, search, status, statusPembayaran, jenisPembayaran string, idRuang, idPelanggan int64, dari, sampai *string, limit, offset int) ([]entity.Penjualan, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+penjualanFrom+penjualanFilter,
		search, status, statusPembayaran, jenisPembayaran, idRuang, idPelanggan, dari, sampai,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count penjualan: %w", err)
	}

	if total == 0 {
		return []entity.Penjualan{}, 0, nil
	}

	// ORDER BY ends in a unique column, so a page boundary between same-day notas
	// cannot repeat or skip one. penjualan_tanggal_id_idx from migration 000022
	// supports it directly.
	const query = `SELECT ` + penjualanReadColumns + penjualanFrom + penjualanFilter + `
		ORDER BY p.tanggal DESC, p.id DESC
		LIMIT $9 OFFSET $10`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, statusPembayaran, jenisPembayaran, idRuang, idPelanggan, dari, sampai,
		limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("select penjualan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Penjualan, 0, limit)

	for rows.Next() {
		var penjualan entity.Penjualan

		if err := scanPenjualanRead(rows, &penjualan); err != nil {
			return nil, 0, fmt.Errorf("scan penjualan: %w", err)
		}

		list = append(list, penjualan)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate penjualan: %w", err)
	}

	return list, total, nil
}

// InsertDetail writes one line.
//
// hpp_satuan_dasar and hpp_total are deliberately absent from the column list: a
// DRAFT has no answer for either. Both are filled by UpdateDetailPosting at Posting,
// from what the outgoing kartu_stok row's RETURNING reported.
//
// There is no unique index on (id_penjualan, id_product) — migration 000022 says
// why: the quota here is the room's balance, checked in total at posting, not a
// quantity held on a parent line. The same product may appear on more than one line.
func (r *PenjualanRepository) InsertDetail(ctx context.Context, db DBTX, detail *entity.PenjualanDetail) error {
	const query = `
		INSERT INTO penjualan_detail (
			id_penjualan, id_product, qty_input, id_satuan_input, faktor_konversi,
			qty_dasar, id_harga_jual, harga_satuan_input, diskon_baris, subtotal
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query,
		detail.IDPenjualan, detail.IDProduct, detail.QtyInput, detail.IDSatuanInput,
		detail.FaktorKonversi, detail.QtyDasar, detail.IDHargaJual,
		detail.HargaSatuanInput, detail.DiskonBaris, detail.Subtotal,
	).Scan(&detail.ID)
	if err != nil {
		return fmt.Errorf("insert penjualan_detail: %w", err)
	}

	return nil
}

// UpdateDetailPosting records what the room's stock actually cost, copied from the
// harga_pokok_satuan and nilai_keluar the outgoing kartu_stok row reported back —
// the application cannot know the moving average before the insert. See
// entity.PenjualanDetail.HPPSatuanDasar.
func (r *PenjualanRepository) UpdateDetailPosting(ctx context.Context, db DBTX, id int64, hppSatuanDasar, hppTotal string) error {
	const query = `
		UPDATE penjualan_detail SET
			hpp_satuan_dasar = $2::NUMERIC,
			hpp_total        = $3::NUMERIC
		WHERE id = $1`

	if _, err := db.ExecContext(ctx, query, id, hppSatuanDasar, hppTotal); err != nil {
		return fmt.Errorf("update penjualan_detail posting: %w", err)
	}

	return nil
}

// DeleteDetail clears every line, for the wholesale replace that
// PUT /penjualan/{id}/detail performs. Only ever reached on a DRAFT.
func (r *PenjualanRepository) DeleteDetail(ctx context.Context, db DBTX, id int64) error {
	const query = `DELETE FROM penjualan_detail WHERE id_penjualan = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete penjualan_detail: %w", err)
	}

	return nil
}

func (r *PenjualanRepository) FindDetail(ctx context.Context, db DBTX, id int64) ([]entity.PenjualanDetail, error) {
	const query = `SELECT ` + penjualanDetailReadColumns + penjualanDetailFrom + `
		WHERE pd.id_penjualan = $1
		ORDER BY pd.id`

	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("select penjualan_detail: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PenjualanDetail, 0, 8)

	for rows.Next() {
		var detail entity.PenjualanDetail

		if err := rows.Scan(
			&detail.ID, &detail.IDPenjualan, &detail.IDProduct, &detail.QtyInput,
			&detail.IDSatuanInput, &detail.FaktorKonversi, &detail.QtyDasar, &detail.IDHargaJual,
			&detail.HargaSatuanInput, &detail.DiskonBaris, &detail.Subtotal,
			&detail.HPPSatuanDasar, &detail.HPPTotal,
			&detail.KodeBarang, &detail.NamaProduct, &detail.NamaSatuan, &detail.NamaSatuanDasar,
		); err != nil {
			return nil, fmt.Errorf("scan penjualan_detail: %w", err)
		}

		list = append(list, detail)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate penjualan_detail: %w", err)
	}

	return list, nil
}

// PiutangBerjalan sums the total of every POSTED KREDIT nota for one customer — the
// running receivable PenjualanUseCase checks against plafon_kredit at posting (isu
// #10 fase 2).
//
// It does not yet subtract anything. penerimaan_pembayaran and retur_penjualan are
// both out of scope for this issue, so every POSTED KREDIT nota's full total counts
// until one of them exists to reduce it — the same "total for now" reading
// entity.PiutangPelanggan documents.
func (r *PenjualanRepository) PiutangBerjalan(ctx context.Context, db DBTX, idPelanggan int64) (string, error) {
	const query = `
		SELECT COALESCE(SUM(total), 0)::TEXT
		FROM penjualan
		WHERE id_pelanggan = $1 AND status = 'POSTED' AND jenis_pembayaran = 'KREDIT'`

	var total string
	if err := db.QueryRowContext(ctx, query, idPelanggan).Scan(&total); err != nil {
		return "", fmt.Errorf("sum piutang berjalan: %w", err)
	}

	return total, nil
}

// FindPiutangPelanggan returns one page of a customer's open KREDIT notas, oldest
// first — the mirror of PembelianRepository.FindUtangSupplier on the receivable
// side.
//
// Only POSTED KREDIT notas: a DRAFT is a typed page and a BATAL one was withdrawn,
// and a TUNAI nota was never a receivable to begin with — RecalculateStatusPembayaran
// makes that one LUNAS the moment it posts. sisa_piutang is total for now, the same
// reading PiutangBerjalan and entity.PiutangPelanggan both document.
func (r *PenjualanRepository) FindPiutangPelanggan(ctx context.Context, db DBTX, idPelanggan int64, limit, offset int) ([]entity.PiutangPelanggan, int64, error) {
	const filter = `
		WHERE p.id_pelanggan = $1 AND p.status = 'POSTED' AND p.jenis_pembayaran = 'KREDIT'`

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM penjualan p`+filter, idPelanggan,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count piutang pelanggan: %w", err)
	}

	if total == 0 {
		return []entity.PiutangPelanggan{}, 0, nil
	}

	const query = `
		SELECT p.id, p.nomor, p.tanggal, p.total::TEXT, p.total::TEXT
		FROM penjualan p` + filter + `
		ORDER BY p.tanggal, p.id
		LIMIT $2 OFFSET $3`

	rows, err := db.QueryContext(ctx, query, idPelanggan, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select piutang pelanggan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PiutangPelanggan, 0, limit)

	for rows.Next() {
		var piutang entity.PiutangPelanggan

		if err := rows.Scan(
			&piutang.IDPenjualan, &piutang.Nomor, &piutang.Tanggal,
			&piutang.Total, &piutang.SisaPiutang,
		); err != nil {
			return nil, 0, fmt.Errorf("scan piutang pelanggan: %w", err)
		}

		list = append(list, piutang)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate piutang pelanggan: %w", err)
	}

	return list, total, nil
}

// penjualanFields lists the scan targets in the order of penjualanColumns, once, so
// the two read paths cannot drift apart from each other or from the constant.
func penjualanFields(penjualan *entity.Penjualan) []any {
	return []any{
		&penjualan.ID, &penjualan.Nomor, &penjualan.Tanggal,
		&penjualan.IDRuang, &penjualan.IDPelanggan,
		&penjualan.Subtotal, &penjualan.DiskonNota, &penjualan.Pembulatan,
		&penjualan.Total, &penjualan.TotalHPP,
		&penjualan.JenisPembayaran, &penjualan.StatusPembayaran, &penjualan.Status,
		&penjualan.CreatedBy, &penjualan.CreatedAt, &penjualan.PostedAt,
		&penjualan.DibatalkanOleh, &penjualan.AlasanBatal,
	}
}

func scanPenjualan(row rowScanner, penjualan *entity.Penjualan) error {
	return row.Scan(penjualanFields(penjualan)...)
}

func scanPenjualanRead(row rowScanner, penjualan *entity.Penjualan) error {
	return row.Scan(append(
		penjualanFields(penjualan), &penjualan.NamaRuang, &penjualan.NamaPelanggan,
	)...)
}
