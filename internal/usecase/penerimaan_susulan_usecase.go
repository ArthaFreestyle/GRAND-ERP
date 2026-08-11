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

// PenerimaanSusulanUseCase owns the follow-up receipt: the second shipment for
// goods that did not arrive with the first.
//
// It adds stock and never a payable. The supplier's invoice was issued in full with
// the first delivery and booked in full then, so what is still outstanding after
// that is goods. That is the whole reason this is a separate document rather than an
// edit: a POSTED pembelian must not change, and kartu_stok is append-only, so
// raising qty_diterima on a posted line is not available — it would break document
// immutability, make cancellation impossible to audit (which kartu_stok rows would
// be reversed?), and erase when the goods actually turned up.
type PenerimaanSusulanUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	SusulanRepository   *repository.PenerimaanSusulanRepository
	PembelianRepository *repository.PembelianRepository
	ProductRepository   *repository.ProductRepository
	KartuStokRepository *repository.KartuStokRepository
	CounterRepository   *repository.DocumentCounterRepository
}

func NewPenerimaanSusulanUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	susulanRepository *repository.PenerimaanSusulanRepository,
	pembelianRepository *repository.PembelianRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
) *PenerimaanSusulanUseCase {
	return &PenerimaanSusulanUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		SusulanRepository:   susulanRepository,
		PembelianRepository: pembelianRepository,
		ProductRepository:   productRepository,
		KartuStokRepository: kartuStokRepository,
		CounterRepository:   counterRepository,
	}
}

// Create opens a DRAFT against one posted purchase.
//
// The supplier and the room are copied from that purchase rather than chosen: the
// supplier is fixed by the document this is a remainder of, and the room was already
// settled there.
func (c *PenerimaanSusulanUseCase) Create(ctx context.Context, request *model.CreatePenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
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

	pembelian, err := c.kunciPembelianPosted(ctx, tx, request.IDPembelian)
	if err != nil {
		return nil, err
	}

	nomor, err := nomorDokumen(ctx, tx, c.CounterRepository, repository.PrefixSusulan, tanggal)
	if err != nil {
		return nil, err
	}

	susulan := &entity.PenerimaanSusulan{
		Nomor:       nomor,
		Tanggal:     tanggal,
		IDPembelian: pembelian.ID,
		IDSupplier:  pembelian.IDSupplier,
		IDRuang:     pembelian.IDRuang,
		Keterangan:  request.Keterangan,
		Status:      entity.StatusSusulanDraft,
		CreatedBy:   request.ActorID,
	}

	if err := c.SusulanRepository.Create(ctx, tx, susulan); err != nil {
		return nil, conflictOnUnique(err, "nomor dokumen sudah dipakai")
	}

	detail, err := c.siapkanDetail(ctx, tx, pembelian.ID, susulan.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, susulan.ID, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather
	// than what was sent.
	return c.detail(ctx, c.DB, susulan.ID)
}

func (c *PenerimaanSusulanUseCase) Get(ctx context.Context, request *model.GetPenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PenerimaanSusulanUseCase) Search(ctx context.Context, request *model.ListPenerimaanSusulanRequest) ([]model.PenerimaanSusulanResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.SusulanRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDPembelian,
		request.TanggalDari, request.TanggalSampai,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PenerimaanSusulanToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT.
func (c *PenerimaanSusulanUseCase) Update(ctx context.Context, request *model.UpdatePenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// tanggal is NOT NULL: changeable, never clearable.
	if request.Tanggal.Clears() {
		return nil, model.Invalid("tanggal cannot be null")
	}

	var patch repository.SusulanPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	patch.SetKeterangan = request.Keterangan.Present
	patch.Keterangan = request.Keterangan.Value

	if patch.Tanggal == nil && !patch.SetKeterangan {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanDraft); err != nil {
		return nil, err
	}

	if err := c.SusulanRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, notFoundOnNoRows(err, "penerimaan susulan not found")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceDetail swaps the whole line set of a DRAFT.
func (c *PenerimaanSusulanUseCase) ReplaceDetail(ctx context.Context, request *model.ReplacePenerimaanSusulanDetailRequest) (*model.PenerimaanSusulanResponse, error) {
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

	susulan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanDraft)
	if err != nil {
		return nil, err
	}

	if _, err := c.kunciPembelianPosted(ctx, tx, susulan.IDPembelian); err != nil {
		return nil, err
	}

	if err := c.SusulanRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, err := c.siapkanDetail(ctx, tx, susulan.IDPembelian, request.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, request.ID, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PenerimaanSusulanUseCase) Ajukan(ctx context.Context, request *model.AjukanPenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanDraft); err != nil {
		return nil, err
	}

	detail, err := c.SusulanRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("penerimaan susulan tanpa baris tidak bisa diajukan")
	}

	if err := c.SusulanRepository.Ajukan(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status penerimaan susulan sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PenerimaanSusulanUseCase) Tolak(ctx context.Context, request *model.TolakPenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanDiajukan); err != nil {
		return nil, err
	}

	if err := c.SusulanRepository.Tolak(ctx, tx, request.ID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(err, "status penerimaan susulan sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Posting writes the goods into stock and closes the document.
//
// The outstanding quantity is re-checked here, under the purchase's row lock, and
// that is the check that counts. The one at create time only produces a friendlier
// error sooner: between a draft being written and posted, another follow-up receipt
// for the same purchase can post and consume the remainder.
func (c *PenerimaanSusulanUseCase) Posting(ctx context.Context, request *model.PostingPenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
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

	susulan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanDiajukan)
	if err != nil {
		return nil, err
	}

	// Locking the purchase serialises every follow-up receipt against the same
	// document, which is what stops two of them both fitting inside a remainder
	// that only one of them fits in.
	pembelian, err := c.kunciPembelianPosted(ctx, tx, susulan.IDPembelian)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the
	// status column being right. Posting twice doubles the stock, and kartu_stok is
	// append-only.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTableSusulan, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("penerimaan susulan ini sudah punya baris kartu stok")
	}

	detail, err := c.SusulanRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("penerimaan susulan tanpa baris tidak bisa diposting")
	}

	for i := range detail {
		if err := c.periksaSisa(ctx, tx, susulan.IDPembelian, detail[i].IDPembelianDetail, detail[i].QtyDasar, i+1); err != nil {
			return nil, err
		}

		qtyInput := detail[i].QtyInput
		satuanInput := detail[i].IDSatuanInput

		kartu := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          pembelian.IDRuang,
			TanggalTransaksi: susulan.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiPenerimaanSusulan,
			StokMasuk:        detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			NilaiMasuk:       detail[i].Nilai,
			RefTable:         entity.RefTableSusulan,
			RefIDTransaksi:   susulan.ID,
			CreatedBy:        request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				"posting ditolak kartu stok: periode sudah TUTUP atau saldo tidak mencukupi",
			)
		}
	}

	if err := c.SusulanRepository.Posting(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status penerimaan susulan sudah berubah")
	}

	// The purchase is POSTED by now and can no longer recompute its own receipt
	// cache, so this is the only place that keeps status_penerimaan honest. It runs
	// after the transition, because the query counts POSTED follow-ups.
	if err := c.PembelianRepository.RecalculateStatusPenerimaan(ctx, tx, susulan.IDPembelian); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted follow-up receipt by appending a reversing row for every
// movement it made, and hands the outstanding quantity back to the purchase.
//
// The same caveat as cancelling a purchase applies: the reversal is valued at the
// current moving average, not at the cost the original row carried, because the
// trigger overwrites the cost on every outgoing row. See PembelianUseCase.Batal.
func (c *PenerimaanSusulanUseCase) Batal(ctx context.Context, request *model.BatalPenerimaanSusulanRequest) (*model.PenerimaanSusulanResponse, error) {
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

	susulan, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSusulanPosted)
	if err != nil {
		return nil, err
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTableSusulan, request.ID)
	if err != nil {
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
			TanggalTransaksi: time.Now(),
			JenisTransaksi:   entity.JenisTransaksiPembatalanTransaksi,
			StokMasuk:        asal[i].StokKeluar,
			StokKeluar:       asal[i].StokMasuk,
			NilaiMasuk:       asal[i].NilaiKeluar,
			RefTable:         entity.RefTableSusulan,
			RefIDTransaksi:   request.ID,
			IDKartuStokAsal:  &idAsal,
			Keterangan:       &keterangan,
			CreatedBy:        request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return nil, invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: barangnya sudah terpakai atau periode sudah TUTUP",
			)
		}
	}

	if err := c.SusulanRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status penerimaan susulan sudah berubah")
	}

	// The quantity goes back to being outstanding, so the purchase's receipt cache
	// has to say KURANG again.
	if err := c.PembelianRepository.RecalculateStatusPenerimaan(ctx, tx, susulan.IDPembelian); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// detail loads a header and its lines. Two queries, independent of how many lines
// come back.
func (c *PenerimaanSusulanUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.PenerimaanSusulanResponse, error) {
	susulan, err := c.SusulanRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "penerimaan susulan not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the
	// key and implying the document was never asked about.
	susulan.Detail, err = c.SusulanRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.PenerimaanSusulanToResponse(susulan), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *PenerimaanSusulanUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.PenerimaanSusulan, error) {
	susulan, err := c.SusulanRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "penerimaan susulan not found")
	}

	if susulan.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, penerimaan susulan sekarang %s", diharapkan, susulan.Status,
		))
	}

	return susulan, nil
}

// kunciPembelianPosted locks the source purchase and insists it is still POSTED.
//
// POSTED is required for two separate reasons, and both matter. Before posting, a
// line carries no harga_pokok_satuan_dasar to copy — the cost is not known until the
// purchase is costed. And the lock is what serialises follow-up receipts against
// each other: without it two of them can both read the same remainder and both
// consume it.
func (c *PenerimaanSusulanUseCase) kunciPembelianPosted(ctx context.Context, tx repository.DBTX, id int64) (*entity.Pembelian, error) {
	pembelian, err := c.PembelianRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	if pembelian.Status != entity.StatusPembelianPosted {
		return nil, model.Conflict(fmt.Sprintf(
			"penerimaan susulan butuh pembelian berstatus POSTED, pembelian ini %s", pembelian.Status,
		))
	}

	return pembelian, nil
}

// siapkanDetail turns request lines into rows: it resolves the source line, checks
// the outstanding quantity, converts to base units, and copies the cost.
func (c *PenerimaanSusulanUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idPembelian, idSusulan int64, requests []model.PenerimaanSusulanDetailRequest) ([]entity.PenerimaanSusulanDetail, error) {
	sumber := make([]*entity.PembelianDetail, len(requests))
	productIDs := make([]int64, len(requests))
	satuanIDs := make([]int64, len(requests))

	// A repeat inside one request would raise 23505 from
	// penerimaan_susulan_detail_baris_uidx, naming an index. Catching it first says
	// what the caller actually got wrong — and without the index, two lines for the
	// same source would each pass the remainder check on their own.
	terlihat := make(map[int64]int, len(requests))

	for i := range requests {
		baris := &requests[i]

		if sebelumnya, ulang := terlihat[baris.IDPembelianDetail]; ulang {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: baris pembelian yang sama sudah dipakai di baris %d", i+1, sebelumnya,
			))
		}

		terlihat[baris.IDPembelianDetail] = i + 1

		detail, err := c.PembelianRepository.FindDetailByID(ctx, tx, baris.IDPembelianDetail)
		if err != nil {
			return nil, notFoundOnNoRows(err, fmt.Sprintf(
				"baris %d: baris pembelian tidak ditemukan", i+1,
			))
		}

		// A line from another purchase would let one document draw down a remainder
		// it has no claim on, and the header's supplier and room would not match it.
		if detail.IDPembelian != idPembelian {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: baris itu milik pembelian lain", i+1,
			))
		}

		if detail.HargaPokokSatuanDasar == nil {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: harga pokok baris pembelian belum terisi", i+1,
			))
		}

		sumber[i] = detail
		productIDs[i] = detail.IDProduct
		satuanIDs[i] = baris.IDSatuanInput
	}

	faktor, err := c.ProductRepository.FindFaktorBatch(ctx, tx, productIDs, satuanIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]entity.PenerimaanSusulanDetail, 0, len(requests))

	for i := range requests {
		baris := &requests[i]
		asal := sumber[i]

		konversi, terdaftar := faktor[repository.FaktorKey{
			IDProduct: asal.IDProduct,
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

		qtyDasarInt := ratKeInt64(qtyDasar)

		if err := c.periksaSisa(ctx, tx, idPembelian, asal.ID, qtyDasarInt, i+1); err != nil {
			return nil, err
		}

		// Cost copied from the source line, never recomputed and never read off the
		// current moving average. What the goods cost was settled by the invoice,
		// and copying it is what makes a purchase and its follow-ups add up to
		// exactly the invoice value.
		hpp := mustParseNumeric(*asal.HargaPokokSatuanDasar)
		nilai := roundNumeric(new(big.Rat).Mul(hpp, ratFromInt(qtyDasarInt)), skalaUang)

		rows = append(rows, entity.PenerimaanSusulanDetail{
			IDPenerimaanSusulan:   idSusulan,
			IDPembelianDetail:     asal.ID,
			QtyInput:              formatNumeric(qtyInput, skalaQty),
			IDSatuanInput:         baris.IDSatuanInput,
			FaktorKonversi:        konversi,
			QtyDasar:              qtyDasarInt,
			HargaPokokSatuanDasar: formatNumeric(hpp, skalaHPP),
			Nilai:                 formatNumeric(nilai, skalaUang),
		})
	}

	return rows, nil
}

// periksaSisa refuses a quantity larger than what the supplier still owes on a line.
//
// The outstanding figure is read fresh rather than passed in, because it counts
// POSTED follow-up receipts and those can appear between a draft being written and
// posted. It reads through FindDetailByID, whose query already subtracts them.
func (c *PenerimaanSusulanUseCase) periksaSisa(ctx context.Context, tx repository.DBTX, idPembelian, idPembelianDetail, qtyDasar int64, nomorBaris int) error {
	asal, err := c.PembelianRepository.FindDetailByID(ctx, tx, idPembelianDetail)
	if err != nil {
		return notFoundOnNoRows(err, fmt.Sprintf("baris %d: baris pembelian tidak ditemukan", nomorBaris))
	}

	if asal.IDPembelian != idPembelian {
		return model.Invalid(fmt.Sprintf("baris %d: baris itu milik pembelian lain", nomorBaris))
	}

	sisa := asal.QtyDasar - asal.QtyDiterimaDasar - asal.QtySusulanDasar

	if sisa <= 0 {
		return model.Invalid(fmt.Sprintf(
			"baris %d: baris pembelian itu sudah diterima lengkap", nomorBaris,
		))
	}

	if qtyDasar > sisa {
		return model.Invalid(fmt.Sprintf(
			"baris %d: qty %d satuan dasar melebihi sisa %d", nomorBaris, qtyDasar, sisa,
		))
	}

	return nil
}

// tulisDetail inserts the lines and refreshes the header's value total.
func (c *PenerimaanSusulanUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, id int64, detail []entity.PenerimaanSusulanDetail) error {
	total := new(big.Rat)

	for i := range detail {
		if err := c.SusulanRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return invalidOnForeignKey(
				conflictOnUnique(err, "baris pembelian yang sama dipakai dua kali"),
				"id_pembelian_detail atau id_satuan_input tidak ada",
			)
		}

		total.Add(total, mustParseNumeric(detail[i].Nilai))
	}

	return c.SusulanRepository.RecalculateTotal(
		ctx, tx, id, formatNumeric(roundNumeric(total, skalaUang), skalaUang),
	)
}
