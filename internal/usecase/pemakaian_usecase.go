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

// PemakaianUseCase owns the internal-usage request — isu #9. Goods leaving for
// repair, office supplies, or a sample: no supplier, no customer, no destination
// room. It is the fifth document to write kartu_stok, and the first whose posting
// takes goods out with no counterparty on the other side at all.
//
// Two things set it apart from every earlier stock writer:
//
//   - The quantity posted is qty_disetujui_dasar, never qty_dasar. What was asked
//     for and what was actually allowed to leave are different numbers, and the
//     approver may trim them per line — see Setujui and Posting.
//   - Its approval stage (DISETUJUI) sits between submission and posting, on top of
//     the ordinary DIAJUKAN, because approving a request decides HOW MUCH may leave
//     while posting records that it actually did, and the two can happen on
//     different days.
//
// It holds no RuangRepository: unlike pembelian and mutasi, isu #9 does not ask for
// id_ruang to be validated against the caller's active unit_kerja, and this module
// does not add that scope on its own.
type PemakaianUseCase struct {
	DB                   *sql.DB
	Log                  *logrus.Logger
	Validate             *validator.Validate
	PemakaianRepository  *repository.PemakaianRepository
	ProductRepository    *repository.ProductRepository
	KartuStokRepository  *repository.KartuStokRepository
	CounterRepository    *repository.DocumentCounterRepository
	PeriodeRepository    *repository.PeriodeRepository
}

func NewPemakaianUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	pemakaianRepository *repository.PemakaianRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
	periodeRepository *repository.PeriodeRepository,
) *PemakaianUseCase {
	return &PemakaianUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		PemakaianRepository: pemakaianRepository,
		ProductRepository:   productRepository,
		KartuStokRepository: kartuStokRepository,
		CounterRepository:   counterRepository,
		PeriodeRepository:   periodeRepository,
	}
}

// Create opens a DRAFT with its lines. Lines are optional here and mandatory on
// ReplaceDetail, the same asymmetry mutasi allows: a request is commonly typed while
// the list is still being put together, and submitting refuses a document with no
// lines anyway.
func (c *PemakaianUseCase) Create(ctx context.Context, request *model.CreatePemakaianRequest) (*model.PemakaianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal, err := time.Parse(dateOnly, request.Tanggal)
	if err != nil {
		return nil, model.Invalid("tanggal harus YYYY-MM-DD")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	nomor, err := nomorDokumen(ctx, tx, c.CounterRepository, repository.PrefixPemakaian, tanggal)
	if err != nil {
		return nil, err
	}

	pemakaian := &entity.Pemakaian{
		Nomor:     nomor,
		Tanggal:   tanggal,
		IDRuang:   request.IDRuang,
		IDPemohon: request.IDPemohon,
		Keperluan: request.Keperluan,
		Status:    entity.StatusPemakaianDraft,
		CreatedBy: request.ActorID,
	}

	if err := c.PemakaianRepository.Create(ctx, tx, pemakaian); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "nomor dokumen sudah dipakai"),
			"id_ruang atau id_pemohon tidak ada",
		)
	}

	detail, err := c.siapkanDetail(ctx, tx, pemakaian.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather
	// than what was sent.
	return c.detail(ctx, c.DB, pemakaian.ID)
}

func (c *PemakaianUseCase) Get(ctx context.Context, request *model.GetPemakaianRequest) (*model.PemakaianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PemakaianUseCase) Search(ctx context.Context, request *model.ListPemakaianRequest) ([]model.PemakaianResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PemakaianRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDRuang, request.IDPemohon,
		request.TanggalDari, request.TanggalSampai, request.TerlamaDulu,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PemakaianToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT. All four fields are NOT NULL columns:
// changeable, never clearable.
func (c *PemakaianUseCase) Update(ctx context.Context, request *model.UpdatePemakaianRequest) (*model.PemakaianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if request.Tanggal.Clears() {
		return nil, model.Invalid("tanggal cannot be null")
	}
	if request.IDRuang.Clears() {
		return nil, model.Invalid("id_ruang cannot be null")
	}
	if request.IDPemohon.Clears() {
		return nil, model.Invalid("id_pemohon cannot be null")
	}
	if request.Keperluan.Clears() {
		return nil, model.Invalid("keperluan cannot be null")
	}

	var patch repository.PemakaianPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	patch.IDRuang = request.IDRuang.Value
	patch.IDPemohon = request.IDPemohon.Value
	patch.Keperluan = request.Keperluan.Value

	if patch.Tanggal == nil && patch.IDRuang == nil && patch.IDPemohon == nil && patch.Keperluan == nil {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDraft); err != nil {
		return nil, err
	}

	if err := c.PemakaianRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnForeignKey(
			notFoundOnNoRows(err, "pemakaian not found"),
			"id_ruang atau id_pemohon tidak ada",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceDetail swaps the whole line set of a DRAFT.
func (c *PemakaianUseCase) ReplaceDetail(ctx context.Context, request *model.ReplacePemakaianDetailRequest) (*model.PemakaianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDraft); err != nil {
		return nil, err
	}

	if err := c.PemakaianRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, err := c.siapkanDetail(ctx, tx, request.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Ajukan hands a draft to the approver. This is the point the document stops being
// editable: everything after it is the approver's decision.
func (c *PemakaianUseCase) Ajukan(ctx context.Context, request *model.AjukanPemakaianRequest) (*model.PemakaianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDraft); err != nil {
		return nil, err
	}

	detail, err := c.PemakaianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("pemakaian tanpa baris tidak bisa diajukan")
	}

	if err := c.PemakaianRepository.Ajukan(ctx, tx, request.ID); err != nil {
		return nil, conflictOnTransisi(err, "status pemakaian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Setujui approves a submission and decides how much of each line may actually
// leave.
//
// pemakaian_penyetuju_check already guarantees the database will refuse an approver
// approving their own request, but that CHECK carries no constraint-specific
// message, so it is caught here first — the same relationship ExistsByKode has to a
// unique index — and kept as the backstop through invalidOnCheck.
//
// A line the caller does not mention defaults to its own qty_dasar in full: the
// ordinary case, where everything asked for is granted.
func (c *PemakaianUseCase) Setujui(ctx context.Context, request *model.SetujuiPemakaianRequest) (*model.PemakaianResponse, error) {
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

	pemakaian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDiajukan)
	if err != nil {
		return nil, err
	}

	if request.ActorID == pemakaian.IDPemohon {
		return nil, model.Invalid("penyetuju tidak boleh sama dengan pemohon")
	}

	detail, err := c.PemakaianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("pemakaian tanpa baris tidak bisa disetujui")
	}

	override := make(map[int64]int64, len(request.Detail))
	dikenal := make(map[int64]bool, len(detail))
	for i := range detail {
		dikenal[detail[i].ID] = true
	}

	for i := range request.Detail {
		baris := &request.Detail[i]
		if !dikenal[baris.IDDetail] {
			return nil, model.Invalid(fmt.Sprintf(
				"id_detail %d tidak ada di dokumen ini", baris.IDDetail,
			))
		}

		override[baris.IDDetail] = baris.QtyDisetujuiDasar
	}

	for i := range detail {
		qty := detail[i].QtyDasar
		if nilai, ada := override[detail[i].ID]; ada {
			qty = nilai
		}

		if qty < 0 || qty > detail[i].QtyDasar {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d (%s): qty_disetujui_dasar harus antara 0 dan %d",
				i+1, detail[i].NamaProduct, detail[i].QtyDasar,
			))
		}

		if err := c.PemakaianRepository.UpdateDetailQtyDisetujui(ctx, tx, detail[i].ID, qty); err != nil {
			return nil, err
		}
	}

	if err := c.PemakaianRepository.Setujui(ctx, tx, request.ID, request.ActorID, request.Catatan); err != nil {
		return nil, conflictOnTransisi(
			invalidOnCheck(err, "penyetuju tidak boleh sama dengan pemohon"),
			"status pemakaian sudah berubah",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Tolak refuses a submission outright. Unlike pembelian this never returns the
// document to DRAFT — see entity.StatusPemakaianDitolak.
func (c *PemakaianUseCase) Tolak(ctx context.Context, request *model.TolakPemakaianRequest) (*model.PemakaianResponse, error) {
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

	pemakaian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDiajukan)
	if err != nil {
		return nil, err
	}

	if request.ActorID == pemakaian.IDPemohon {
		return nil, model.Invalid("penyetuju tidak boleh sama dengan pemohon")
	}

	if err := c.PemakaianRepository.Tolak(ctx, tx, request.ID, request.ActorID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(
			invalidOnCheck(err, "penyetuju tidak boleh sama dengan pemohon"),
			"status pemakaian sudah berubah",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Posting records that the approved goods actually left, and writes kartu_stok.
//
// Only qty_disetujui_dasar reaches the ledger, never qty_dasar — the single most
// expensive thing to get wrong in this module. A line whose approved quantity is
// nil (impossible once Setujui has run) or zero is skipped rather than inserted as a
// movement of nothing; kartu_stok must never carry a row that moved nothing.
func (c *PemakaianUseCase) Posting(ctx context.Context, request *model.PostingPemakaianRequest) (*model.PemakaianResponse, error) {
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

	pemakaian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianDisetujui)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the
	// status column being right. Posting twice doubles the stock leaving.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTablePemakaian, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("pemakaian ini sudah punya baris kartu stok")
	}

	detail, err := c.PemakaianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	baris := make([]entity.PemakaianDetail, 0, len(detail))
	for i := range detail {
		if detail[i].QtyDisetujuiDasar == nil || *detail[i].QtyDisetujuiDasar == 0 {
			continue
		}

		baris = append(baris, detail[i])
	}

	if len(baris) == 0 {
		return nil, model.Invalid("pemakaian tanpa baris yang disetujui tidak bisa diposting")
	}

	if err := c.kunciJalurStok(ctx, tx, pemakaian.IDRuang, baris, pemakaian.Tanggal); err != nil {
		return nil, err
	}

	// Checked so the refusal can name the period; the trigger stays the guard.
	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, pemakaian.Tanggal); err != nil {
		return nil, err
	}

	if err := c.periksaSaldo(ctx, tx, pemakaian, baris); err != nil {
		return nil, err
	}

	totalHPP := new(big.Rat)

	for i := range baris {
		qtyInput := baris[i].QtyInput
		satuanInput := baris[i].IDSatuanInput
		qtyDisetujui := *baris[i].QtyDisetujuiDasar

		kartu := &entity.KartuStok{
			IDBarang:         baris[i].IDProduct,
			IDRuang:          pemakaian.IDRuang,
			TanggalTransaksi: pemakaian.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiPemakaian,
			StokKeluar:       qtyDisetujui,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// Zero, not a cost of our own. This is an outgoing row: the trigger
			// computes what leaves from the running average, and nilai_masuk is
			// NOT NULL so it has to be sent as an explicit zero.
			NilaiMasuk:     "0",
			RefTable:       entity.RefTablePemakaian,
			RefIDTransaksi: pemakaian.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				fmt.Sprintf(
					"posting ditolak kartu stok: stok %s di ruang %d tidak mencukupi atau periode sudah TUTUP",
					baris[i].NamaProduct, pemakaian.IDRuang,
				),
			)
		}

		if err := c.PemakaianRepository.UpdateDetailPosting(
			ctx, tx, baris[i].ID, kartu.HargaPokokSatuan, kartu.NilaiKeluar,
		); err != nil {
			return nil, err
		}

		totalHPP.Add(totalHPP, mustParseNumeric(kartu.NilaiKeluar))
	}

	if err := c.PemakaianRepository.RecalculateTotalHPP(
		ctx, tx, request.ID, formatNumeric(roundNumeric(totalHPP, skalaUang), skalaUang),
	); err != nil {
		return nil, err
	}

	if err := c.PemakaianRepository.Posting(ctx, tx, request.ID); err != nil {
		return nil, conflictOnTransisi(err, "status pemakaian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted request by appending a reversing row for every movement it
// made — the goods come back to the room they left from.
//
// Unlike Posting, this can never drive a balance negative: it only ever adds stock
// back, so it needs no periksaSaldo pre-check. The balance locks are still taken up
// front, for the same reason Posting takes them — two concurrent cancellations
// touching the same products in a different line order are a textbook ABBA.
//
// Dated today rather than on the document, like every reversal in this codebase, so
// a request whose period has since closed can still be voided — into the current
// period. See PembelianUseCase.Batal for the cost that comes with it.
func (c *PemakaianUseCase) Batal(ctx context.Context, request *model.BatalPemakaianRequest) (*model.PemakaianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPemakaianPosted); err != nil {
		return nil, err
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTablePemakaian, request.ID)
	if err != nil {
		return nil, err
	}

	tanggalPembalik := time.Now()

	if err := c.kunciJalurPembalik(ctx, tx, asal, tanggalPembalik); err != nil {
		return nil, err
	}

	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, tanggalPembalik); err != nil {
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
			RefTable:         entity.RefTablePemakaian,
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

	if err := c.PemakaianRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status pemakaian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// kunciJalurStok takes every balance lock Posting's inserts will need, up front, in
// the canonical order every writer in the system uses — periode first, then
// (id_barang, id_ruang).
//
// Every line here shares one room, unlike mutasi, but the lock is not optional for
// that reason: two pemakaian documents naming the same products in a different line
// order are a textbook ABBA even inside a single room, because the trigger takes one
// advisory lock per insert rather than one per document. Taking every key up front,
// sorted, makes that impossible rather than merely unlikely.
func (c *PemakaianUseCase) kunciJalurStok(ctx context.Context, tx repository.DBTX, idRuang int64, baris []entity.PemakaianDetail, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(baris))
	for i := range baris {
		keys = append(keys, repository.SaldoKey{IDBarang: baris[i].IDProduct, IDRuang: idRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// kunciJalurPembalik is kunciJalurStok for a cancellation, where the pairs to lock
// are read off the rows being reversed rather than off the document's lines.
func (c *PemakaianUseCase) kunciJalurPembalik(ctx context.Context, tx repository.DBTX, asal []entity.KartuStok, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(asal))
	for i := range asal {
		keys = append(keys, repository.SaldoKey{IDBarang: asal[i].IDBarang, IDRuang: asal[i].IDRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// periksaSaldo refuses a request the room cannot cover, before the trigger has to.
//
// Not a guard — the trigger is, deciding inside the advisory lock that no reader can
// get in front of. This is the second module after mutasi whose stock can genuinely
// run short: a negative-stock rejection here is an everyday occurrence, not a
// theoretical defence like retur_pembelian's, so the message has to name the
// product and the room rather than arrive as the trigger's constraint-free RAISE.
//
// It runs after kunciJalurStok, which matters: with the balance locks already held,
// what it reads cannot move underneath it for the rest of the transaction.
func (c *PemakaianUseCase) periksaSaldo(ctx context.Context, tx repository.DBTX, pemakaian *entity.Pemakaian, baris []entity.PemakaianDetail) error {
	diminta := make(map[int64]int64, len(baris))
	barangIDs := make([]int64, 0, len(baris))
	ruangIDs := make([]int64, 0, len(baris))

	for i := range baris {
		if _, ada := diminta[baris[i].IDProduct]; !ada {
			barangIDs = append(barangIDs, baris[i].IDProduct)
			ruangIDs = append(ruangIDs, pemakaian.IDRuang)
		}

		diminta[baris[i].IDProduct] += *baris[i].QtyDisetujuiDasar
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, tx, barangIDs, ruangIDs)
	if err != nil {
		return err
	}

	dilaporkan := make(map[int64]bool, len(diminta))

	for i := range baris {
		idProduct := baris[i].IDProduct
		if dilaporkan[idProduct] {
			continue
		}

		dilaporkan[idProduct] = true

		tersedia := saldo[repository.SaldoKey{IDBarang: idProduct, IDRuang: pemakaian.IDRuang}].StokAkhir

		if diminta[idProduct] > tersedia {
			return model.Invalid(fmt.Sprintf(
				"baris %d: %s butuh %d satuan dasar di ruang %d, saldo hanya %d",
				i+1, baris[i].NamaProduct, diminta[idProduct], pemakaian.IDRuang, tersedia,
			))
		}
	}

	return nil
}

// detail loads a header and its lines. Two queries, independent of how many lines
// come back.
func (c *PemakaianUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.PemakaianResponse, error) {
	pemakaian, err := c.PemakaianRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pemakaian not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the
	// key and implying the document was never asked about.
	pemakaian.Detail, err = c.PemakaianRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.PemakaianToResponse(pemakaian), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *PemakaianUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.Pemakaian, error) {
	pemakaian, err := c.PemakaianRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pemakaian not found")
	}

	if pemakaian.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, pemakaian sekarang %s", diharapkan, pemakaian.Status,
		))
	}

	return pemakaian, nil
}

// siapkanDetail turns request lines into rows: it resolves the conversion factor and
// converts the quantity to base units. No cost is computed here — see
// entity.PemakaianDetail.HPPSatuanDasar for why it cannot be known until posting.
func (c *PemakaianUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idPemakaian int64, requests []model.PemakaianDetailRequest) ([]entity.PemakaianDetail, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	productIDs := make([]int64, len(requests))
	satuanIDs := make([]int64, len(requests))

	for i := range requests {
		productIDs[i] = requests[i].IDProduct
		satuanIDs[i] = requests[i].IDSatuanInput
	}

	faktor, err := c.ProductRepository.FindFaktorBatch(ctx, tx, productIDs, satuanIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]entity.PemakaianDetail, 0, len(requests))

	for i := range requests {
		baris := &requests[i]

		konversi, terdaftar := faktor[repository.FaktorKey{
			IDProduct: baris.IDProduct,
			IDSatuan:  baris.IDSatuanInput,
		}]
		if !terdaftar {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: satuan itu belum terdaftar di product_satuan produk ini", i+1,
			))
		}

		qtyInput, err := parseNumeric(baris.QtyInput)
		if err != nil {
			return nil, err
		}
		if qtyInput.Sign() <= 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: qty_input harus lebih dari nol", i+1))
		}

		qtyDasar := new(big.Rat).Mul(qtyInput, ratFromInt(konversi))
		if !isWholeNumber(qtyDasar) {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_input x faktor menghasilkan %s satuan dasar, bukan bilangan bulat",
				i+1, formatNumeric(qtyDasar, skalaQty),
			))
		}

		rows = append(rows, entity.PemakaianDetail{
			IDPemakaian:    idPemakaian,
			IDProduct:      baris.IDProduct,
			QtyInput:       formatNumeric(qtyInput, skalaQty),
			IDSatuanInput:  baris.IDSatuanInput,
			FaktorKonversi: konversi,
			QtyDasar:       ratKeInt64(qtyDasar),
			Keterangan:     baris.Keterangan,
		})
	}

	return rows, nil
}

// tulisDetail inserts the lines.
func (c *PemakaianUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, detail []entity.PemakaianDetail) error {
	for i := range detail {
		if err := c.PemakaianRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return invalidOnForeignKey(err, "id_product atau id_satuan_input tidak ada")
		}
	}

	return nil
}
