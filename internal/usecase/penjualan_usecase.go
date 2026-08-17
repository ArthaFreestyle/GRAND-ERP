package usecase

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// PenjualanUseCase owns the sales nota — isu #10. It is the sixth document to write
// kartu_stok and the first to take goods out to an outside party with money moving
// on the other side of it: pembelian forms a payable, mutasi forms nothing at all,
// this is the first to form a receivable, and only on a KREDIT nota.
//
// Three things set it apart from every earlier stock writer:
//
//   - HPP is never typed. hpp_satuan_dasar, hpp_total, and total_hpp are filled at
//     posting from what the outgoing kartu_stok rows actually reported — the exact
//     rule mutasi_detail.HargaPokokSatuanDasar and pemakaian's HPPTotal already
//     follow. See Posting.
//   - The price billed is a snapshot, never forced to equal product_harga_jual.
//     id_harga_jual only records which price-list version proposed the figure, and
//     is validated against the line's own product, satuan, and the document's date
//     when supplied at all.
//   - It is the second module after pemakaian whose stock can genuinely run short
//     as an everyday event, not a theoretical defence — a cashier types 10 when the
//     shelf holds 7, and that is a fact of the counter, not a bug.
//
// No DIAJUKAN: a cashier cannot make a customer wait at the counter for a
// supervisor to approve a cash sale typed in seconds. The two-person control moves
// entirely to the cancellation side instead — CASHIER creates, types lines, and
// posts; SUPERADMIN alone may void. See entity.StatusPenjualanDraft.
//
// PelangganRepository is borrowed for exactly one narrow read: plafon_kredit and the
// running receivable behind it, checked at Posting for a KREDIT nota (isu #10 fase
// 2).
//
// isu #10 itself did not ask for id_ruang to be validated against the
// caller's active unit_kerja, and for a long time this module followed
// pemakaian in deliberately not adding that scope on its own initiative. isu
// #21 fase 2 closed that: once a second outlet is really running, an
// unscoped id_ruang here is an authorization hole, not a missing convenience
// — a cashier from unit A could otherwise post a nota against unit B's room
// with nothing to refuse it.
type PenjualanUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	PenjualanRepository *repository.PenjualanRepository
	ProductRepository   *repository.ProductRepository
	PelangganRepository *repository.PelangganRepository
	KartuStokRepository *repository.KartuStokRepository
	CounterRepository   *repository.DocumentCounterRepository
	PeriodeRepository   *repository.PeriodeRepository
	// RuangRepository was first borrowed only for LockShared (isu #15);
	// periksaRuangUnitAktif/IDUnitKerjaByID (isu #21 fase 2) now use it too.
	RuangRepository *repository.RuangRepository
	// StokOpnameRepository is the same narrow borrow every kartu_stok writer gets,
	// for periksaRuangBeku's message.
	StokOpnameRepository *repository.StokOpnameRepository
	// UnitKerjaRepository resolves the kode a document number carries — isu #21
	// fase 1 — read off id_ruang, not the caller's active unit_kerja.
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewPenjualanUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	penjualanRepository *repository.PenjualanRepository,
	productRepository *repository.ProductRepository,
	pelangganRepository *repository.PelangganRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
	periodeRepository *repository.PeriodeRepository,
	ruangRepository *repository.RuangRepository,
	stokOpnameRepository *repository.StokOpnameRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *PenjualanUseCase {
	return &PenjualanUseCase{
		DB:                   db,
		Log:                  log,
		Validate:             validate,
		PenjualanRepository:  penjualanRepository,
		ProductRepository:    productRepository,
		PelangganRepository:  pelangganRepository,
		KartuStokRepository:  kartuStokRepository,
		CounterRepository:    counterRepository,
		PeriodeRepository:    periodeRepository,
		RuangRepository:      ruangRepository,
		StokOpnameRepository: stokOpnameRepository,
		UnitKerjaRepository:  unitKerjaRepository,
	}
}

// Create opens a DRAFT nota, optionally with its lines.
//
// Lines are optional here and mandatory on ReplaceDetail, the same asymmetry
// mutasi and pemakaian allow: a nota is commonly typed while items are still being
// scanned, and posting refuses a document with no lines anyway.
func (c *PenjualanUseCase) Create(ctx context.Context, request *model.CreatePenjualanRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal, err := time.Parse(dateOnly, request.Tanggal)
	if err != nil {
		return nil, model.Invalid("tanggal harus YYYY-MM-DD")
	}

	jenisPembayaran := pilihanAtau(request.JenisPembayaran, entity.JenisPembayaranTunai)

	// penjualan_kredit_pelanggan_check would reject this too, but a CHECK
	// violation names a constraint and cannot say which field is wrong — the same
	// relationship ExistsByKode has to a unique index.
	if jenisPembayaran == entity.JenisPembayaranKredit && request.IDPelanggan == nil {
		return nil, model.Invalid("id_pelanggan wajib diisi untuk penjualan KREDIT")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	if err := periksaRuangUnitAktif(ctx, tx, c.RuangRepository, request.AktifIDUnitKerja, request.IDRuang); err != nil {
		return nil, err
	}

	nomor, err := nomorDokumenUntukRuang(
		ctx, tx, c.CounterRepository, c.RuangRepository, c.UnitKerjaRepository,
		repository.PrefixPenjualan, tanggal, request.IDRuang,
	)
	if err != nil {
		return nil, err
	}

	penjualan := &entity.Penjualan{
		Nomor:            nomor,
		Tanggal:          tanggal,
		IDRuang:          request.IDRuang,
		IDPelanggan:      request.IDPelanggan,
		DiskonNota:       nilaiAtauNol(request.DiskonNota),
		Pembulatan:       nilaiAtauNol(request.Pembulatan),
		JenisPembayaran:  jenisPembayaran,
		StatusPembayaran: entity.StatusPembayaranBelum,
		Status:           entity.StatusPenjualanDraft,
		CreatedBy:        request.ActorID,
	}

	if err := c.PenjualanRepository.Create(ctx, tx, penjualan); err != nil {
		return nil, invalidOnForeignKey(
			invalidOnCheck(
				conflictOnUnique(err, "nomor dokumen sudah dipakai"),
				"id_pelanggan wajib diisi untuk penjualan KREDIT",
			),
			"id_ruang atau id_pelanggan tidak ada",
		)
	}

	detail, subtotal, err := c.siapkanDetail(ctx, tx, penjualan.ID, request.Detail, tanggal)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := c.simpanTotal(ctx, tx, penjualan.ID, subtotal, penjualan.DiskonNota, penjualan.Pembulatan); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather
	// than what was sent.
	return c.detail(ctx, c.DB, penjualan.ID, nil)
}

func (c *PenjualanUseCase) Get(ctx context.Context, request *model.GetPenjualanRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, request.AktifIDUnitKerja)
}

func (c *PenjualanUseCase) Search(ctx context.Context, request *model.ListPenjualanRequest) ([]model.PenjualanResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PenjualanRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.StatusPembayaran, request.JenisPembayaran,
		request.IDRuang, request.IDPelanggan, request.TanggalDari, request.TanggalSampai,
		request.AktifIDUnitKerja, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PenjualanToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT.
//
// id_ruang, id_pelanggan, and jenis_pembayaran may all move — no penjualan_detail
// row names any of them, so nothing is left behind pointing at the wrong place. The
// KREDIT-needs-a-pelanggan rule is re-checked here against the *effective* values,
// the same pattern MutasiUseCase.Update uses for id_ruang_asal <> id_ruang_tujuan: a
// patch that changes neither field cannot have broken anything, but one that flips
// jenis_pembayaran to KREDIT while id_pelanggan is already null (or being cleared in
// the same request) must be caught before it reaches the database.
func (c *PenjualanUseCase) Update(ctx context.Context, request *model.UpdatePenjualanRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	patch, err := patchPenjualanDariRequest(request)
	if err != nil {
		return nil, err
	}

	if patch.Tanggal == nil && patch.IDRuang == nil && !patch.SetIDPelanggan &&
		patch.JenisPembayaran == nil && patch.DiskonNota == nil && patch.Pembulatan == nil {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	penjualan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPenjualanDraft)
	if err != nil {
		return nil, err
	}

	jenisPembayaran := penjualan.JenisPembayaran
	if patch.JenisPembayaran != nil {
		jenisPembayaran = *patch.JenisPembayaran
	}

	idPelanggan := penjualan.IDPelanggan
	if patch.SetIDPelanggan {
		idPelanggan = patch.IDPelanggan
	}

	if jenisPembayaran == entity.JenisPembayaranKredit && idPelanggan == nil {
		return nil, model.Invalid("id_pelanggan wajib diisi untuk penjualan KREDIT")
	}

	// Only when id_ruang is actually moving, and only the new value: a room
	// once created never changes its unit_kerja (ruang has no PATCH), so a
	// stored id_ruang the patch leaves untouched was already valid.
	if patch.IDRuang != nil {
		if err := periksaRuangUnitAktif(ctx, tx, c.RuangRepository, request.AktifIDUnitKerja, *patch.IDRuang); err != nil {
			return nil, err
		}
	}

	// diskon_nota/pembulatan are validated against the stored subtotal — a header
	// patch never touches the lines, so the subtotal a patch has to respect is
	// whatever ReplaceDetail last wrote.
	effDiskon := penjualan.DiskonNota
	if patch.DiskonNota != nil {
		effDiskon = *patch.DiskonNota
	}
	effPembulatan := penjualan.Pembulatan
	if patch.Pembulatan != nil {
		effPembulatan = *patch.Pembulatan
	}

	total, err := hitungTotalPenjualan(mustParseNumeric(penjualan.Subtotal), effDiskon, effPembulatan)
	if err != nil {
		return nil, err
	}

	if err := c.PenjualanRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnForeignKey(
			invalidOnCheck(
				notFoundOnNoRows(err, "penjualan not found"),
				"id_pelanggan wajib diisi untuk penjualan KREDIT",
			),
			"id_ruang atau id_pelanggan tidak ada",
		)
	}

	if err := c.PenjualanRepository.SimpanTotal(
		ctx, tx, request.ID, penjualan.Subtotal, formatNumeric(total, skalaUang),
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// ReplaceDetail swaps the whole line set of a DRAFT and recomputes subtotal/total
// from the new lines.
func (c *PenjualanUseCase) ReplaceDetail(ctx context.Context, request *model.ReplacePenjualanDetailRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	penjualan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPenjualanDraft)
	if err != nil {
		return nil, err
	}

	if err := c.PenjualanRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, subtotal, err := c.siapkanDetail(ctx, tx, request.ID, request.Detail, penjualan.Tanggal)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := c.simpanTotal(ctx, tx, request.ID, subtotal, penjualan.DiskonNota, penjualan.Pembulatan); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Posting moves the goods out, writes HPP from what the ledger actually reported,
// and — for a KREDIT nota — is the one moment plafon_kredit is enforced.
//
// The order inside the transaction matters. Balance locks and the negative-stock
// pre-check come first, following mutasi and pemakaian; the plafon check comes
// after them and before any kartu_stok row is written, so a nota refused for credit
// reasons leaves no stock movement to unwind.
func (c *PenjualanUseCase) Posting(ctx context.Context, request *model.PostingPenjualanRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	penjualan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPenjualanDraft)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the
	// status column being right. Posting twice would double what left the shelf.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTablePenjualan, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("penjualan ini sudah punya baris kartu stok")
	}

	detail, err := c.PenjualanRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("penjualan tanpa baris tidak bisa diposting")
	}

	if err := c.kunciJalurStok(ctx, tx, penjualan.IDRuang, detail, penjualan.Tanggal); err != nil {
		return nil, err
	}

	// Checked so the refusal can name the period; the trigger stays the guard.
	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, penjualan.Tanggal); err != nil {
		return nil, err
	}

	// Same relationship to the freeze that periksaPeriode has to a closed period —
	// isu #15. Penjualan inherits this without a line of its own asking for it, the
	// same way mutasi and pemakaian already did.
	if err := periksaRuangBeku(ctx, tx, c.StokOpnameRepository, penjualan.IDRuang); err != nil {
		return nil, err
	}

	if err := c.periksaSaldo(ctx, tx, penjualan.IDRuang, detail); err != nil {
		return nil, err
	}

	if penjualan.JenisPembayaran == entity.JenisPembayaranKredit {
		if err := c.periksaPlafon(ctx, tx, penjualan); err != nil {
			return nil, err
		}
	}

	totalHPP := new(big.Rat)

	for i := range detail {
		qtyInput := detail[i].QtyInput
		satuanInput := detail[i].IDSatuanInput

		kartu := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          penjualan.IDRuang,
			TanggalTransaksi: penjualan.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiPenjualan,
			StokKeluar:       detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// Zero, not a cost of our own. This is an outgoing row: the trigger
			// computes what leaves from the running average, and nilai_masuk is
			// NOT NULL so it has to be sent as an explicit zero.
			NilaiMasuk:     "0",
			RefTable:       entity.RefTablePenjualan,
			RefIDTransaksi: penjualan.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				fmt.Sprintf(
					"posting ditolak kartu stok: stok %s di ruang %d tidak mencukupi atau periode sudah TUTUP",
					detail[i].NamaProduct, penjualan.IDRuang,
				),
			)
		}

		if err := c.PenjualanRepository.UpdateDetailPosting(
			ctx, tx, detail[i].ID, kartu.HargaPokokSatuan, kartu.NilaiKeluar,
		); err != nil {
			return nil, err
		}

		totalHPP.Add(totalHPP, mustParseNumeric(kartu.NilaiKeluar))
	}

	if err := c.PenjualanRepository.RecalculateTotalHPP(
		ctx, tx, request.ID, formatNumeric(roundNumeric(totalHPP, skalaUang), skalaUang),
	); err != nil {
		return nil, err
	}

	if err := c.PenjualanRepository.RecalculateStatusPembayaran(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := c.PenjualanRepository.Posting(ctx, tx, request.ID); err != nil {
		return nil, conflictOnTransisi(err, "status penjualan sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Batal voids a posted nota by appending a reversing row for every movement it
// made — the goods come back to the room they left from.
//
// Dated today rather than on the document, like every reversal in this codebase, so
// a nota whose period has since closed can still be voided — into the current
// period. See PembelianUseCase.Batal for the cost that comes with it.
//
// A posted payment allocation has to be voided first — isu #20, closing half of the
// TODO that used to sit here. The money (or, for an uncashed giro, the paper) is
// pointed at this nota; cancelling it here would leave that allocation claiming to
// settle a nota that no longer says it is owed anything. The other half of the
// original TODO — a POSTED retur_penjualan blocking cancellation the same way
// HasPostedRetur does on the payable side — stays open: retur_penjualan is still out
// of scope, and no module points at penjualan that way yet.
func (c *PenjualanUseCase) Batal(ctx context.Context, request *model.BatalPenjualanRequest) (*model.PenjualanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	penjualan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPenjualanPosted)
	if err != nil {
		return nil, err
	}

	// An uncashed giro counts too: it reduces no receivable, but it is still paper
	// pointed at this nota, and it would be unexplainable when it clears against a
	// nota that no longer claims to be owed anything.
	adaPembayaran, err := c.PenjualanRepository.HasPostedAlokasi(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}
	if adaPembayaran {
		return nil, model.Conflict("batalkan penerimaan pembayaran yang dialokasikan ke nota ini lebih dulu")
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTablePenjualan, request.ID)
	if err != nil {
		return nil, err
	}

	tanggalPembalik := time.Now()

	if err := c.kunciJalurPembalik(ctx, tx, penjualan.IDRuang, asal, tanggalPembalik); err != nil {
		return nil, err
	}

	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, tanggalPembalik); err != nil {
		return nil, err
	}

	if err := periksaRuangBeku(ctx, tx, c.StokOpnameRepository, penjualan.IDRuang); err != nil {
		return nil, err
	}

	for i := range asal {
		// Skip rows that are themselves reversals, so a document can never be
		// unwound twice over.
		if asal[i].IDKartuStokAsal != nil {
			continue
		}

		keterangan := "pembatalan " + request.AlasanBatal
		idAsal := asal[i].ID

		pembalik := &entity.KartuStok{
			IDBarang:         asal[i].IDBarang,
			IDRuang:          asal[i].IDRuang,
			TanggalTransaksi: tanggalPembalik,
			JenisTransaksi:   entity.JenisTransaksiPembatalanTransaksi,
			StokMasuk:        asal[i].StokKeluar,
			StokKeluar:       asal[i].StokMasuk,
			NilaiMasuk:       asal[i].NilaiKeluar,
			RefTable:         entity.RefTablePenjualan,
			RefIDTransaksi:   request.ID,
			IDKartuStokAsal:  &idAsal,
			Keterangan:       &keterangan,
			CreatedBy:        request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return nil, invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: periode sudah TUTUP",
			)
		}
	}

	if err := c.PenjualanRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status penjualan sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// periksaPlafon refuses a KREDIT nota that would push a customer's running
// receivable past plafon_kredit — isu #10 fase 2. Checked at Posting, under the
// document's own row lock, never at draft: a draft is not a sale, and refusing one
// that might still be edited down before it ever posts would be premature.
//
// plafon_kredit IS NULL means unlimited, the same reading it carries everywhere
// else pelanggan.plafon_kredit is read in this codebase.
//
// Unlike periksaSaldo, this has no CHECK or trigger behind it — no constraint can
// compare a limit against a running SUM computed across other rows. That makes this
// the actual guard rather than a friendlier message ahead of one, and it does not
// close every race: two KREDIT notas for the same customer, posted at the same
// moment, can both read the same running total and both pass. Isu #10 does not ask
// for a lock closing that window, and none is added here; if it matters in
// practice, the fix is an advisory lock keyed on id_pelanggan, taken before this
// read, the same shape KartuStokRepository.KunciSaldo already uses for
// (id_barang, id_ruang).
//
// No SUPERADMIN override exists. Posting already sits behind CASHIER alone — see
// entity.StatusPenjualanDraft — and adding a bypass would need a second actor at
// the one moment this codebase deliberately kept to one. If a real need for an
// override shows up, it belongs on PostingPenjualanRequest as an explicit, recorded
// field — never a silent skip.
func (c *PenjualanUseCase) periksaPlafon(ctx context.Context, tx repository.DBTX, penjualan *entity.Penjualan) error {
	if penjualan.IDPelanggan == nil {
		// Unreachable once penjualan_kredit_pelanggan_check has passed, but this
		// function does not get to assume the trigger already ran.
		return nil
	}

	pelanggan, err := c.PelangganRepository.FindByID(ctx, tx, *penjualan.IDPelanggan)
	if err != nil {
		return notFoundOnNoRows(err, "pelanggan not found")
	}

	if pelanggan.PlafonKredit == nil {
		return nil
	}

	plafon := mustParseNumeric(*pelanggan.PlafonKredit)

	berjalan, err := c.PenjualanRepository.PiutangBerjalan(ctx, tx, *penjualan.IDPelanggan)
	if err != nil {
		return err
	}

	setelahNota := new(big.Rat).Add(mustParseNumeric(berjalan), mustParseNumeric(penjualan.Total))

	if setelahNota.Cmp(plafon) > 0 {
		return model.Invalid(fmt.Sprintf(
			"plafon_kredit %s terlampaui: piutang berjalan %s ditambah nota ini %s",
			formatNumeric(plafon, skalaUang), berjalan, formatNumeric(mustParseNumeric(penjualan.Total), skalaUang),
		))
	}

	return nil
}

// kunciJalurStok takes every balance lock Posting's inserts will need, up front, in
// the canonical order every writer in the system uses — periode first, then
// (id_barang, id_ruang). Every line here shares one room, but the lock is not
// optional for that reason: two penjualan documents naming the same products in a
// different line order are a textbook ABBA even inside a single room, because the
// trigger takes one advisory lock per insert rather than one per document. See
// PemakaianUseCase.kunciJalurStok for the identical argument.
func (c *PenjualanUseCase) kunciJalurStok(ctx context.Context, tx repository.DBTX, idRuang int64, detail []entity.PenjualanDetail, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	// isu #15: taken before KunciSaldo, like mutasi, pemakaian, and stok_opname's own
	// posting, so the frozen status periksaRuangBeku reads afterwards cannot move
	// underneath the rest of the transaction.
	if err := c.RuangRepository.LockShared(ctx, tx, idRuang); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(detail))
	for i := range detail {
		keys = append(keys, repository.SaldoKey{IDBarang: detail[i].IDProduct, IDRuang: idRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// kunciJalurPembalik is kunciJalurStok for a cancellation, where the pairs to lock
// are read off the rows being reversed rather than off the document's lines.
func (c *PenjualanUseCase) kunciJalurPembalik(ctx context.Context, tx repository.DBTX, idRuang int64, asal []entity.KartuStok, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	if err := c.RuangRepository.LockShared(ctx, tx, idRuang); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(asal))
	for i := range asal {
		keys = append(keys, repository.SaldoKey{IDBarang: asal[i].IDBarang, IDRuang: asal[i].IDRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// periksaSaldo refuses a nota the room cannot cover, before the trigger has to.
//
// Not a guard — the trigger is, deciding inside the advisory lock that no reader
// can get in front of. This is the second module after pemakaian whose stock can
// genuinely run short as an everyday event: a cashier typing 10 when the shelf
// holds 7 is not a bug, and the message has to name the product and the room
// rather than arrive as the trigger's constraint-free RAISE — the reader is
// standing in front of the customer.
//
// It runs after kunciJalurStok, which matters: with the balance locks already
// held, what it reads cannot move underneath it for the rest of the transaction.
func (c *PenjualanUseCase) periksaSaldo(ctx context.Context, tx repository.DBTX, idRuang int64, detail []entity.PenjualanDetail) error {
	diminta := make(map[int64]int64, len(detail))
	barangIDs := make([]int64, 0, len(detail))
	ruangIDs := make([]int64, 0, len(detail))

	for i := range detail {
		if _, ada := diminta[detail[i].IDProduct]; !ada {
			barangIDs = append(barangIDs, detail[i].IDProduct)
			ruangIDs = append(ruangIDs, idRuang)
		}

		diminta[detail[i].IDProduct] += detail[i].QtyDasar
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, tx, barangIDs, ruangIDs)
	if err != nil {
		return err
	}

	dilaporkan := make(map[int64]bool, len(diminta))

	for i := range detail {
		idProduct := detail[i].IDProduct
		if dilaporkan[idProduct] {
			continue
		}

		dilaporkan[idProduct] = true

		tersedia := saldo[repository.SaldoKey{IDBarang: idProduct, IDRuang: idRuang}].StokAkhir

		if diminta[idProduct] > tersedia {
			return model.Invalid(fmt.Sprintf(
				"baris %d: %s butuh %d satuan dasar di ruang %d, saldo hanya %d",
				i+1, detail[i].NamaProduct, diminta[idProduct], idRuang, tersedia,
			))
		}
	}

	return nil
}

// detail loads a header and its lines. Two queries, independent of how many lines
// come back.
//
// aktifIDUnitKerja scopes the read — isu #21 fase 2 — and is threaded as a
// parameter rather than read from ambient state: Get passes the caller's real
// active unit, but every write-path call passes nil, since a caller who just
// acted on a document is by construction allowed to see the response their
// own action produced.
func (c *PenjualanUseCase) detail(ctx context.Context, db repository.DBTX, id int64, aktifIDUnitKerja *int64) (*model.PenjualanResponse, error) {
	penjualan, err := c.PenjualanRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "penjualan not found")
	}

	if diLuarUnitAktif(penjualan.IDUnitKerjaRuang, aktifIDUnitKerja) {
		return nil, model.NotFound("penjualan not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the
	// key and implying the document was never asked about.
	penjualan.Detail, err = c.PenjualanRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.PenjualanToResponse(penjualan), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *PenjualanUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.Penjualan, error) {
	penjualan, err := c.PenjualanRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "penjualan not found")
	}

	if penjualan.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, penjualan sekarang %s", diharapkan, penjualan.Status,
		))
	}

	return penjualan, nil
}

// siapkanDetail turns request lines into rows: it resolves the conversion factor,
// converts the quantity to base units, validates a supplied id_harga_jual, and
// computes each line's subtotal — returning the document-wide sum of those
// subtotals alongside the rows, since every caller needs both.
//
// Unlike pembelian there is no proportional allocation of any kind: no freight, no
// nota discount split across lines, no PPN share. subtotal is a straight sum, and
// every money figure here is exact math/big.Rat arithmetic, never float64.
func (c *PenjualanUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idPenjualan int64, requests []model.PenjualanDetailRequest, tanggal time.Time) ([]entity.PenjualanDetail, *big.Rat, error) {
	subtotalDokumen := new(big.Rat)

	if len(requests) == 0 {
		return nil, subtotalDokumen, nil
	}

	productIDs := make([]int64, len(requests))
	satuanIDs := make([]int64, len(requests))

	for i := range requests {
		productIDs[i] = requests[i].IDProduct
		satuanIDs[i] = requests[i].IDSatuanInput
	}

	faktor, err := c.ProductRepository.FindFaktorBatch(ctx, tx, productIDs, satuanIDs)
	if err != nil {
		return nil, nil, err
	}

	// One query for the whole basket, exactly what FindHargaBerlakuBatch was built
	// for (see its doc comment on ProductRepository) — a pair absent from the
	// result simply has no price in force on this date.
	harga, err := c.ProductRepository.FindHargaBerlakuBatch(ctx, tx, productIDs, satuanIDs, tanggal)
	if err != nil {
		return nil, nil, err
	}

	rows := make([]entity.PenjualanDetail, 0, len(requests))

	for i := range requests {
		baris := &requests[i]

		konversi, terdaftar := faktor[repository.FaktorKey{
			IDProduct: baris.IDProduct,
			IDSatuan:  baris.IDSatuanInput,
		}]
		if !terdaftar {
			return nil, nil, model.Invalid(fmt.Sprintf(
				"baris %d: satuan itu belum terdaftar di product_satuan produk ini", i+1,
			))
		}

		qtyInput, err := parseNumeric(baris.QtyInput)
		if err != nil {
			return nil, nil, err
		}
		if qtyInput.Sign() <= 0 {
			return nil, nil, model.Invalid(fmt.Sprintf("baris %d: qty_input harus lebih dari nol", i+1))
		}

		qtyDasar := new(big.Rat).Mul(qtyInput, ratFromInt(konversi))
		if !isWholeNumber(qtyDasar) {
			return nil, nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_input x faktor menghasilkan %s satuan dasar, bukan bilangan bulat",
				i+1, formatNumeric(qtyDasar, skalaQty),
			))
		}

		// id_harga_jual is a proposal, not a requirement — harga_satuan_input is
		// what is actually billed either way. When a version is named, it has to
		// be the one actually in force for this line's own product and satuan on
		// this document's date, or the reference is more misleading than none at
		// all.
		if baris.IDHargaJual != nil {
			berlaku, ada := harga[repository.HargaBerlakuKey{
				IDProduct: baris.IDProduct, IDSatuan: baris.IDSatuanInput,
			}]
			if !ada || berlaku.IDHargaJual != *baris.IDHargaJual {
				return nil, nil, model.Invalid(fmt.Sprintf(
					"baris %d: id_harga_jual bukan versi yang berlaku untuk produk dan satuan ini pada tanggal dokumen",
					i+1,
				))
			}
		}

		hargaSatuan, err := parseNumeric(baris.HargaSatuanInput)
		if err != nil {
			return nil, nil, err
		}
		if hargaSatuan.Sign() < 0 {
			return nil, nil, model.Invalid(fmt.Sprintf("baris %d: harga_satuan_input tidak boleh negatif", i+1))
		}

		diskonBaris, err := parseNumeric(nilaiAtauNol(baris.DiskonBaris))
		if err != nil {
			return nil, nil, err
		}
		if diskonBaris.Sign() < 0 {
			return nil, nil, model.Invalid(fmt.Sprintf("baris %d: diskon_baris tidak boleh negatif", i+1))
		}

		subtotalBaris := new(big.Rat).Sub(new(big.Rat).Mul(qtyInput, hargaSatuan), diskonBaris)
		if subtotalBaris.Sign() < 0 {
			return nil, nil, model.Invalid(fmt.Sprintf(
				"baris %d: diskon_baris melebihi qty_input x harga_satuan_input", i+1,
			))
		}

		subtotalDokumen.Add(subtotalDokumen, subtotalBaris)

		rows = append(rows, entity.PenjualanDetail{
			IDPenjualan:      idPenjualan,
			IDProduct:        baris.IDProduct,
			QtyInput:         formatNumeric(qtyInput, skalaQty),
			IDSatuanInput:    baris.IDSatuanInput,
			FaktorKonversi:   konversi,
			QtyDasar:         ratKeInt64(qtyDasar),
			IDHargaJual:      baris.IDHargaJual,
			HargaSatuanInput: formatNumeric(hargaSatuan, skalaUang),
			DiskonBaris:      formatNumeric(diskonBaris, skalaUang),
			Subtotal:         formatNumeric(roundNumeric(subtotalBaris, skalaUang), skalaUang),
		})
	}

	return rows, subtotalDokumen, nil
}

// tulisDetail inserts the lines.
func (c *PenjualanUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, detail []entity.PenjualanDetail) error {
	for i := range detail {
		if err := c.PenjualanRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return invalidOnForeignKey(err, "id_product, id_satuan_input, atau id_harga_jual tidak ada")
		}
	}

	return nil
}

// simpanTotal validates diskon_nota against the subtotal the lines just produced,
// computes total, and writes both — the one place Create and ReplaceDetail meet,
// since both change subtotal and both therefore have to revalidate the discount
// against it.
func (c *PenjualanUseCase) simpanTotal(ctx context.Context, tx repository.DBTX, id int64, subtotal *big.Rat, diskonNota, pembulatan string) error {
	total, err := hitungTotalPenjualan(subtotal, diskonNota, pembulatan)
	if err != nil {
		return err
	}

	return c.PenjualanRepository.SimpanTotal(
		ctx, tx, id, formatNumeric(roundNumeric(subtotal, skalaUang), skalaUang), formatNumeric(total, skalaUang),
	)
}

// hitungTotalPenjualan validates diskon_nota against subtotal and returns the
// resulting total, rejecting a discount steeper than the subtotal it is taken
// against and a total that would go negative — "nota bertotal negatif bukan
// penjualan". Pure arithmetic, no I/O, shared by Create, Update, and ReplaceDetail
// so the rule is checked identically regardless of which of the three moved.
func hitungTotalPenjualan(subtotal *big.Rat, diskonNotaText, pembulatanText string) (*big.Rat, error) {
	diskon, err := parseNumeric(diskonNotaText)
	if err != nil {
		return nil, err
	}
	if diskon.Sign() < 0 {
		return nil, model.Invalid("diskon_nota tidak boleh negatif")
	}
	if diskon.Cmp(subtotal) > 0 {
		return nil, model.Invalid("diskon_nota tidak boleh melebihi subtotal")
	}

	pembulatan, err := parseNumeric(pembulatanText)
	if err != nil {
		return nil, err
	}

	total := new(big.Rat).Add(new(big.Rat).Sub(subtotal, diskon), pembulatan)
	if total.Sign() < 0 {
		return nil, model.Invalid("total penjualan tidak boleh negatif")
	}

	return total, nil
}

// patchPenjualanDariRequest maps the DTO's Optional fields onto the repository
// patch, and rejects an explicit null on a NOT NULL column — the same discipline
// pembelian's patchDariRequest follows, and for the identical reason: COALESCE
// would silently turn `{"jenis_pembayaran": null}` into a no-op, and the caller
// would never learn their request did nothing.
func patchPenjualanDariRequest(request *model.UpdatePenjualanRequest) (repository.PenjualanPatch, error) {
	var patch repository.PenjualanPatch

	for nama, wajib := range map[string]bool{
		"tanggal":          request.Tanggal.Clears(),
		"id_ruang":         request.IDRuang.Clears(),
		"jenis_pembayaran": request.JenisPembayaran.Clears(),
		"diskon_nota":      request.DiskonNota.Clears(),
		"pembulatan":       request.Pembulatan.Clears(),
	} {
		if wajib {
			return patch, model.Invalid(nama + " cannot be null")
		}
	}

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return patch, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	patch.IDRuang = request.IDRuang.Value
	patch.SetIDPelanggan = request.IDPelanggan.Present
	patch.IDPelanggan = request.IDPelanggan.Value
	patch.JenisPembayaran = request.JenisPembayaran.Value
	patch.DiskonNota = request.DiskonNota.Value
	patch.Pembulatan = request.Pembulatan.Value

	return patch, nil
}
