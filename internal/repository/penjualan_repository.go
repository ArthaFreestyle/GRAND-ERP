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
// r.id_unit_kerja rides along for isu #21 fase 2 (read-path scoping); it costs
// nothing extra since penjualanFrom already joins ruang for its name.
const penjualanReadColumns = `p.id, p.nomor, p.tanggal, p.id_ruang, p.id_pelanggan,
	p.subtotal::TEXT, p.diskon_nota::TEXT, p.pembulatan::TEXT, p.total::TEXT, p.total_hpp::TEXT,
	p.jenis_pembayaran, p.status_pembayaran, p.status, p.created_by, p.created_at, p.posted_at,
	p.dibatalkan_oleh, p.alasan_batal, r.nama_ruang, pel.nama, r.id_unit_kerja`

// penjualanFrom joins id_ruang INNER — that column is NOT NULL, so a nota without a
// room cannot exist — and id_pelanggan LEFT: a cash sale legitimately has none.
const penjualanFrom = `
	FROM penjualan p
	JOIN ruang r ON r.id = p.id_ruang
	LEFT JOIN pelanggan pel ON pel.id = p.id_pelanggan`

// penjualanAlokasiEfektif totals what has actually been received against a nota —
// the receivable-side mirror of pembelianAlokasiEfektif in pembelian_repository.go
// (isu #20).
//
// "Effective" carries the same giro rule: a POSTED payment counts, except a giro
// that has not cleared. **An uncashed giro is not a payment.** Counting one would
// mark a nota settled on the strength of a promise, and the customer would go on
// disputing a balance the system believes is gone. A bounced giro (TOLAK) never
// counts either, and needs no reversal precisely because it never counted.
//
// A correlated subquery rather than a grouped LEFT JOIN, for the same reason
// pembelianAlokasiEfektif is one: it is driven by the outer rows and uses an index
// on pembayaran_alokasi(id_penjualan), so it costs one index lookup per nota
// instead of aggregating both tables before joining.
//
// Correlated on the alias `p`, so every query using it has to name the sales table
// `p` — penjualanFrom, RecalculateStatusPembayaran, and PiutangBerjalan all do.
//
// It yields NUMERIC, not text: the fallback is cast to NUMERIC(20,2) rather than
// left as the integer literal 0, so a nota nobody has paid reports "0.00" like
// every other money field instead of a bare "0". Callers that want text add their
// own ::TEXT.
const penjualanAlokasiEfektif = `COALESCE((
		SELECT SUM(pa.jumlah)
		FROM pembayaran_alokasi pa
		JOIN penerimaan_pembayaran pp ON pp.id = pa.id_penerimaan_pembayaran
		WHERE pa.id_penjualan = p.id
		  AND pp.status = 'POSTED'
		  AND (pp.metode <> 'GIRO' OR pp.status_giro = 'CAIR')
	), 0)::NUMERIC(20, 2)`

// penjualanKreditRetur is the receivable-side placeholder for retur_penjualan's
// credit against a nota — the counterpart of pembelianKreditRetur, deliberately
// left at zero.
//
// retur_penjualan is out of scope for isu #20, and the asymmetry with the payable
// side is worth spelling out rather than silently omitting: pembelianKreditRetur
// exists because a purchase's harga pokok carries a freight share the supplier
// never received, so the credit could never be the return's own total. Here there
// is no such gap — retur_penjualan_detail has carried both harga_satuan_input and
// hpp_satuan_dasar since migration 000006, so a sales return's credit has every
// figure it needs already sitting on its own rows and can most likely just be its
// own total once that module exists. Kept as a named fragment rather than a bare 0
// so RecalculateStatusPembayaran, PiutangBerjalan, and FindPiutangPelanggan only
// have to gain a real query here, not a new shape, the day retur_penjualan is
// built.
const penjualanKreditRetur = `0::NUMERIC(20, 2)`

// penjualanFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$9 and pagination follows after it.
//
// $9 is the active-unit scope (isu #21 fase 2, mirroring mutasiFilter's own
// unit clause): NULL means unrestricted (a global grant, or no active
// context), matching periksaRuangUnitAktif's write-side rule.
const penjualanFilter = `
	WHERE ($1 = '' OR p.nomor ILIKE '%' || $1 || '%')
	  AND ($2 = '' OR p.status = $2)
	  AND ($3 = '' OR p.status_pembayaran = $3)
	  AND ($4 = '' OR p.jenis_pembayaran = $4)
	  AND ($5 = 0 OR p.id_ruang = $5)
	  AND ($6 = 0 OR p.id_pelanggan = $6)
	  AND ($7::DATE IS NULL OR p.tanggal >= $7::DATE)
	  AND ($8::DATE IS NULL OR p.tanggal < ($8::DATE + INTERVAL '1 day'))
	  AND ($9::BIGINT IS NULL OR r.id_unit_kerja = $9)`

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
// counterpart of PembelianRepository.RecalculateStatusPembayaran, and, since isu
// #20, a full cache rather than a two-value placeholder.
//
// A TUNAI nota is LUNAS by construction: the money changed hands at the counter and
// no allocation document exists, or ever will, to record that — see the "uncashed
// giro" trap this module does NOT have, in CLAUDE.md. Only Posting calls this for a
// TUNAI nota, before the header's own status flips to POSTED, so the CASE does not
// need to test p.status itself to mean "this nota, once posted, is settled".
//
// A KREDIT nota now answers BELUM/SEBAGIAN/LUNAS from effective allocations —
// penjualanAlokasiEfektif — plus the still-zero retur placeholder,
// penjualanKreditRetur. Everything that can change the answer calls this: posting
// and voiding a payment, clearing and rejecting a giro (isu #20 fase 3).
func (r *PenjualanRepository) RecalculateStatusPembayaran(ctx context.Context, db DBTX, id int64) error {
	const query = `
		UPDATE penjualan p SET status_pembayaran = CASE
			WHEN p.jenis_pembayaran = 'TUNAI' THEN 'LUNAS'
			WHEN p.total <= (` + penjualanAlokasiEfektif + `)
			                + (` + penjualanKreditRetur + `) THEN 'LUNAS'
			WHEN (` + penjualanAlokasiEfektif + `)
			     + (` + penjualanKreditRetur + `) > 0 THEN 'SEBAGIAN'
			ELSE 'BELUM'
		END
		WHERE p.id = $1`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("recalculate penjualan status_pembayaran: %w", err)
	}

	return nil
}

// HasPostedAlokasi reports whether a POSTED payment allocation still points at this
// nota — the receivable-side mirror of PembelianRepository.HasPostedAlokasi.
//
// A POSTED payment counts here regardless of giro status: an uncashed giro reduces
// no receivable, but it is still paper pointed at this nota, and it would be
// unexplainable when it clears against a nota that no longer exists to be settled.
func (r *PenjualanRepository) HasPostedAlokasi(ctx context.Context, db DBTX, idPenjualan int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1
			FROM pembayaran_alokasi a
			JOIN penerimaan_pembayaran pp ON pp.id = a.id_penerimaan_pembayaran
			WHERE a.id_penjualan = $1 AND pp.status = 'POSTED'
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, idPenjualan).Scan(&exists); err != nil {
		return false, fmt.Errorf("check penerimaan_pembayaran alokasi POSTED: %w", err)
	}

	return exists, nil
}

// SisaPiutang carries what one nota's receivable position is, read under its own
// row lock — the receivable-side mirror of repository.SisaUtang.
type SisaPiutang struct {
	IDPelanggan        *int64
	JenisPembayaran    string
	Status             string
	Total              string
	JumlahDialokasikan string
	NilaiKreditRetur   string
}

// FindSisaPiutang reads a nota's receivable position.
//
// Call it **after** taking the row lock with LockByID, never instead of it. The
// lock is what makes the answer usable: two payments can otherwise both read the
// same remaining balance and both allocate against it, and the sum of allocations
// would quietly exceed what was owed. Holding the nota's row lock forces them to
// queue, because every path that changes this nota's receivable takes the same
// lock first.
func (r *PenjualanRepository) FindSisaPiutang(ctx context.Context, db DBTX, idPenjualan int64) (*SisaPiutang, error) {
	const query = `
		SELECT p.id_pelanggan, p.jenis_pembayaran, p.status, p.total::TEXT,
		       (` + penjualanAlokasiEfektif + `)::TEXT, (` + penjualanKreditRetur + `)::TEXT
		FROM penjualan p
		WHERE p.id = $1`

	sisa := new(SisaPiutang)

	err := db.QueryRowContext(ctx, query, idPenjualan).Scan(
		&sisa.IDPelanggan, &sisa.JenisPembayaran, &sisa.Status, &sisa.Total,
		&sisa.JumlahDialokasikan, &sisa.NilaiKreditRetur,
	)
	if err != nil {
		return nil, err
	}

	return sisa, nil
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
func (r *PenjualanRepository) Search(ctx context.Context, db DBTX, search, status, statusPembayaran, jenisPembayaran string, idRuang, idPelanggan int64, dari, sampai *string, aktifIDUnitKerja *int64, limit, offset int) ([]entity.Penjualan, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+penjualanFrom+penjualanFilter,
		search, status, statusPembayaran, jenisPembayaran, idRuang, idPelanggan, dari, sampai,
		aktifIDUnitKerja,
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
		LIMIT $10 OFFSET $11`

	rows, err := db.QueryContext(
		ctx, query,
		search, status, statusPembayaran, jenisPembayaran, idRuang, idPelanggan, dari, sampai,
		aktifIDUnitKerja, limit, offset,
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

// PiutangBerjalan sums the outstanding balance of every POSTED KREDIT nota for one
// customer — the running receivable PenjualanUseCase checks against plafon_kredit
// at posting (isu #10 fase 2).
//
// Since isu #20 this subtracts effective allocations and the (still-zero) retur
// placeholder from each nota's own total, so a customer who has paid down a nota is
// no longer charged its full amount against their limit — the ratchet isu #20 was
// written to remove. Before that fix this summed raw totals with nothing to reduce
// them, since penerimaan_pembayaran did not exist yet.
func (r *PenjualanRepository) PiutangBerjalan(ctx context.Context, db DBTX, idPelanggan int64) (string, error) {
	const query = `
		SELECT COALESCE(SUM(
			p.total - (` + penjualanAlokasiEfektif + `) - (` + penjualanKreditRetur + `)
		), 0)::TEXT
		FROM penjualan p
		WHERE p.id_pelanggan = $1 AND p.status = 'POSTED' AND p.jenis_pembayaran = 'KREDIT'`

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
// makes that one LUNAS the moment it posts. Since isu #20, sisa_piutang is the real
// outstanding balance — total minus effective allocations minus the (still-zero)
// retur placeholder — rather than the "total for now" figure this returned before
// penerimaan_pembayaran existed. The response shape itself is unchanged, exactly as
// isu #20 asks: only what feeds sisa_piutang got real.
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
		SELECT p.id, p.nomor, p.tanggal, p.total::TEXT,
		       (p.total - (` + penjualanAlokasiEfektif + `) - (` + penjualanKreditRetur + `))::TEXT
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

// LabaKotor sums gross margin — total minus total_hpp — over POSTED notas, grouped
// by the calendar month of p.tanggal — isu #22 fase 3.
//
// This is the one report in that issue reading a document table rather than
// kartu_stok, and that is deliberate rather than an inconsistency: total_hpp is
// already a snapshot copied from kartu_stok's own RETURNING at Posting and frozen
// there (PenjualanUseCase.Posting), so re-deriving it from kartu_stok here would pay
// for a second, more expensive query to land on the same number this one already has
// cheaply.
//
// BATAL notas are excluded by the same status filter every other read in this module
// uses; retur_penjualan does not exist yet, so nothing here reduces a month's margin
// for a sale that later came back — the day that module ships, its credit belongs in
// this SUM alongside total_hpp.
//
// aktifIDUnitKerja scopes by the room a nota was posted from — isu #12 fase 6 applied
// to this new read, following every other list in this issue: nil is unrestricted,
// otherwise a EXISTS against ruang keeps a nota outside the caller's active unit from
// ever reaching the SUM.
func (r *PenjualanRepository) LabaKotor(
	ctx context.Context, db DBTX, dari, sampai *string, aktifIDUnitKerja *int64,
) ([]entity.LabaKotorBaris, error) {
	const query = `
		SELECT to_char(date_trunc('month', p.tanggal), 'YYYY-MM'),
			SUM(p.total)::TEXT, SUM(p.total_hpp)::TEXT, SUM(p.total - p.total_hpp)::TEXT
		FROM penjualan p
		WHERE p.status = 'POSTED'
		  AND ($1::DATE IS NULL OR p.tanggal >= $1::DATE)
		  AND ($2::DATE IS NULL OR p.tanggal < ($2::DATE + INTERVAL '1 day'))
		  AND ($3::BIGINT IS NULL OR EXISTS (
			SELECT 1 FROM ruang r WHERE r.id = p.id_ruang AND r.id_unit_kerja = $3
		  ))
		GROUP BY date_trunc('month', p.tanggal)
		ORDER BY date_trunc('month', p.tanggal)`

	rows, err := db.QueryContext(ctx, query, dari, sampai, aktifIDUnitKerja)
	if err != nil {
		return nil, fmt.Errorf("select laba kotor: %w", err)
	}
	defer rows.Close()

	list := make([]entity.LabaKotorBaris, 0, 12)

	for rows.Next() {
		var baris entity.LabaKotorBaris

		if err := rows.Scan(&baris.Bulan, &baris.TotalPenjualan, &baris.TotalHPP, &baris.LabaKotor); err != nil {
			return nil, fmt.Errorf("scan laba kotor: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate laba kotor: %w", err)
	}

	return list, nil
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
		&penjualan.IDUnitKerjaRuang,
	)...)
}
