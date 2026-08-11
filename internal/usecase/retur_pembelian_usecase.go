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

// ReturPembelianUseCase owns the purchase return: goods sent back to the supplier.
//
// It is the mirror of PenerimaanSusulanUseCase, and deliberately built as one. Both
// point at pembelian_detail rows of a POSTED purchase, both copy their cost from
// there rather than deriving one, and both draw down a quota held on that line. The
// goods simply move the other way, so where a follow-up receipt writes stok_masuk this
// writes stok_keluar — and that single difference is what makes cancelling this
// document, and cancelling the purchase behind it, the delicate part.
//
// The quota is not the same quota. A follow-up receipt consumes what the supplier
// still owes; a return consumes what physically arrived. Returning goods does not
// reopen the supplier's obligation to deliver them, so
// PembelianRepository.RecalculateStatusPenerimaan is never called from here.
type ReturPembelianUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	ReturRepository     *repository.ReturPembelianRepository
	PembelianRepository *repository.PembelianRepository
	ProductRepository   *repository.ProductRepository
	KartuStokRepository *repository.KartuStokRepository
	CounterRepository   *repository.DocumentCounterRepository
}

func NewReturPembelianUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	returRepository *repository.ReturPembelianRepository,
	pembelianRepository *repository.PembelianRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
) *ReturPembelianUseCase {
	return &ReturPembelianUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		ReturRepository:     returRepository,
		PembelianRepository: pembelianRepository,
		ProductRepository:   productRepository,
		KartuStokRepository: kartuStokRepository,
		CounterRepository:   counterRepository,
	}
}

// Create opens a DRAFT against one posted purchase.
//
// The supplier and the room are copied from that purchase rather than chosen: the
// supplier is whoever sold the goods, and the room is where the purchase put them.
func (c *ReturPembelianUseCase) Create(ctx context.Context, request *model.CreateReturPembelianRequest) (*model.ReturPembelianResponse, error) {
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

	nomor, err := nomorDokumen(ctx, tx, c.CounterRepository, repository.PrefixRetur, tanggal)
	if err != nil {
		return nil, err
	}

	alasan := request.Alasan

	retur := &entity.ReturPembelian{
		Nomor:       nomor,
		Tanggal:     tanggal,
		IDPembelian: pembelian.ID,
		IDSupplier:  pembelian.IDSupplier,
		IDRuang:     pembelian.IDRuang,
		Alasan:      &alasan,
		Status:      entity.StatusReturDraft,
		CreatedBy:   request.ActorID,
	}

	if err := c.ReturRepository.Create(ctx, tx, retur); err != nil {
		return nil, conflictOnUnique(err, "nomor dokumen sudah dipakai")
	}

	detail, err := c.siapkanDetail(ctx, tx, pembelian.ID, retur.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, retur.ID, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather
	// than what was sent.
	return c.detail(ctx, c.DB, retur.ID)
}

func (c *ReturPembelianUseCase) Get(ctx context.Context, request *model.GetReturPembelianRequest) (*model.ReturPembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *ReturPembelianUseCase) Search(ctx context.Context, request *model.ListReturPembelianRequest) ([]model.ReturPembelianResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.ReturRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDPembelian, request.IDSupplier,
		request.TanggalDari, request.TanggalSampai,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.ReturPembelianToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT.
func (c *ReturPembelianUseCase) Update(ctx context.Context, request *model.UpdateReturPembelianRequest) (*model.ReturPembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// tanggal is NOT NULL: changeable, never clearable.
	if request.Tanggal.Clears() {
		return nil, model.Invalid("tanggal cannot be null")
	}

	// alasan is nullable in the schema but required by policy — the create path insists
	// on one, so allowing a patch to clear it would leave documents that could never
	// have been created that way.
	if request.Alasan.Clears() {
		return nil, model.Invalid("alasan cannot be null")
	}

	var patch repository.ReturPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	patch.SetAlasan = request.Alasan.Set()
	patch.Alasan = request.Alasan.Value

	if patch.Tanggal == nil && !patch.SetAlasan {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturDraft); err != nil {
		return nil, err
	}

	if err := c.ReturRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, notFoundOnNoRows(err, "retur pembelian not found")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceDetail swaps the whole line set of a DRAFT.
func (c *ReturPembelianUseCase) ReplaceDetail(ctx context.Context, request *model.ReplaceReturPembelianDetailRequest) (*model.ReturPembelianResponse, error) {
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

	retur, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturDraft)
	if err != nil {
		return nil, err
	}

	if _, err := c.kunciPembelianPosted(ctx, tx, retur.IDPembelian); err != nil {
		return nil, err
	}

	if err := c.ReturRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, err := c.siapkanDetail(ctx, tx, retur.IDPembelian, request.ID, request.Detail)
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

func (c *ReturPembelianUseCase) Ajukan(ctx context.Context, request *model.AjukanReturPembelianRequest) (*model.ReturPembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturDraft); err != nil {
		return nil, err
	}

	detail, err := c.ReturRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("retur pembelian tanpa baris tidak bisa diajukan")
	}

	if err := c.ReturRepository.Ajukan(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status retur pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *ReturPembelianUseCase) Tolak(ctx context.Context, request *model.TolakReturPembelianRequest) (*model.ReturPembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturDiajukan); err != nil {
		return nil, err
	}

	if err := c.ReturRepository.Tolak(ctx, tx, request.ID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(err, "status retur pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Posting takes the goods out of stock and closes the document.
//
// The returnable quantity is re-checked here, under the purchase's row lock, and that
// is the check that counts. The one at create time only produces a friendlier error
// sooner: between a draft being written and posted, another return for the same
// purchase can post and consume what was left.
//
// One thing the numbers here will not agree on, and it is not a bug: this document's
// `nilai` is the invoice cost of the goods, while the kartu_stok row records them
// leaving at the moving average in force right now. kartu_stok_hitung_saldo overwrites
// nilai_keluar and harga_pokok_satuan on every outgoing row, so a cost supplied by the
// application is discarded — deliberately, because those goods were mixed with older
// stock the moment they arrived and there is no longer a separable batch to price.
func (c *ReturPembelianUseCase) Posting(ctx context.Context, request *model.PostingReturPembelianRequest) (*model.ReturPembelianResponse, error) {
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

	retur, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturDiajukan)
	if err != nil {
		return nil, err
	}

	// Locking the purchase serialises every return against the same document, which is
	// what stops two of them both fitting inside a quantity that only one of them fits
	// in.
	pembelian, err := c.kunciPembelianPosted(ctx, tx, retur.IDPembelian)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the status
	// column being right. Posting twice takes the goods out twice, and kartu_stok is
	// append-only.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTableReturPembelian, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("retur pembelian ini sudah punya baris kartu stok")
	}

	detail, err := c.ReturRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("retur pembelian tanpa baris tidak bisa diposting")
	}

	for i := range detail {
		if err := c.periksaDapatDiretur(ctx, tx, retur.IDPembelian, detail[i].IDPembelianDetail, detail[i].QtyDasar, i+1); err != nil {
			return nil, err
		}

		qtyInput := detail[i].QtyInput
		satuanInput := detail[i].IDSatuanInput

		kartu := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          pembelian.IDRuang,
			TanggalTransaksi: retur.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiReturPembelian,
			StokKeluar:       detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// Zero, not the line's own nilai. This is an outgoing row: the trigger
			// computes what leaves from the running average, and nilai_masuk is NOT NULL
			// so it has to be sent as an explicit zero rather than left empty.
			NilaiMasuk:     "0",
			RefTable:       entity.RefTableReturPembelian,
			RefIDTransaksi: retur.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				"posting ditolak kartu stok: stok di ruang itu tidak mencukupi atau periode sudah TUTUP",
			)
		}
	}

	if err := c.ReturRepository.Posting(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status retur pembelian sudah berubah")
	}

	// pembelian.status_penerimaan is deliberately NOT recomputed. It answers whether
	// everything on the invoice arrived, and sending goods back afterwards does not
	// make the delivery incomplete — nor does it entitle anyone to a follow-up
	// shipment. The two quotas share a source line and nothing else.

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted return by appending a reversing row for every movement it made:
// the goods come back into stock.
//
// Valued at the current moving average, like every reversal in this codebase, because
// the trigger owns the cost of an outgoing row and therefore of what its reversal
// brings back. See PembelianUseCase.Batal.
func (c *ReturPembelianUseCase) Batal(ctx context.Context, request *model.BatalReturPembelianRequest) (*model.ReturPembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusReturPosted); err != nil {
		return nil, err
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTableReturPembelian, request.ID)
	if err != nil {
		return nil, err
	}

	for i := range asal {
		// Skip rows that are themselves reversals, so a document can never be unwound
		// twice over.
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
			// Mirrored: what went out comes back in, valued at what the ledger recorded
			// it leaving for. That is the one figure that makes the pair sum to zero.
			StokMasuk:       asal[i].StokKeluar,
			StokKeluar:      asal[i].StokMasuk,
			NilaiMasuk:      asal[i].NilaiKeluar,
			RefTable:        entity.RefTableReturPembelian,
			RefIDTransaksi:  request.ID,
			IDKartuStokAsal: &idAsal,
			Keterangan:      &keterangan,
			CreatedBy:       request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return nil, invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: periode sudah TUTUP",
			)
		}
	}

	if err := c.ReturRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status retur pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// detail loads a header and its lines. Two queries, independent of how many lines come
// back.
func (c *ReturPembelianUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.ReturPembelianResponse, error) {
	retur, err := c.ReturRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "retur pembelian not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the key
	// and implying the document was never asked about.
	retur.Detail, err = c.ReturRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.ReturPembelianToResponse(retur), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *ReturPembelianUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.ReturPembelian, error) {
	retur, err := c.ReturRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "retur pembelian not found")
	}

	if retur.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, retur pembelian sekarang %s", diharapkan, retur.Status,
		))
	}

	return retur, nil
}

// kunciPembelianPosted locks the source purchase and insists it is still POSTED.
//
// POSTED is required for three reasons here. Before posting, a line carries no
// harga_pokok_satuan_dasar to copy and nothing has physically arrived to send back. And
// the lock is what serialises returns against each other: without it two of them can
// both read the same returnable quantity and both consume it.
func (c *ReturPembelianUseCase) kunciPembelianPosted(ctx context.Context, tx repository.DBTX, id int64) (*entity.Pembelian, error) {
	pembelian, err := c.PembelianRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	if pembelian.Status != entity.StatusPembelianPosted {
		return nil, model.Conflict(fmt.Sprintf(
			"retur pembelian butuh pembelian berstatus POSTED, pembelian ini %s", pembelian.Status,
		))
	}

	return pembelian, nil
}

// siapkanDetail turns request lines into rows: it resolves the source line, checks how
// much may still go back, converts to base units, and copies the cost.
func (c *ReturPembelianUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idPembelian, idRetur int64, requests []model.ReturPembelianDetailRequest) ([]entity.ReturPembelianDetail, error) {
	sumber := make([]*entity.PembelianDetail, len(requests))
	productIDs := make([]int64, len(requests))
	satuanIDs := make([]int64, len(requests))

	// A repeat inside one request would raise 23505 from
	// retur_pembelian_detail_baris_uidx, naming an index. Catching it first says what
	// the caller actually got wrong — and without the index, two lines for the same
	// source would each pass the quantity check on their own.
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

		// A line from another purchase would let one document draw down a quantity it
		// has no claim on, and the header's supplier and room would not match it.
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

	rows := make([]entity.ReturPembelianDetail, 0, len(requests))

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

		if err := c.periksaDapatDiretur(ctx, tx, idPembelian, asal.ID, qtyDasarInt, i+1); err != nil {
			return nil, err
		}

		// Cost copied from the source line, never recomputed and never read off the
		// current moving average. That is what migration 000005 says this table is for:
		// without it a purchase and its return do not cancel out.
		hpp := mustParseNumeric(*asal.HargaPokokSatuanDasar)
		nilai := roundNumeric(new(big.Rat).Mul(hpp, ratFromInt(qtyDasarInt)), skalaUang)

		rows = append(rows, entity.ReturPembelianDetail{
			IDReturPembelian:      idRetur,
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

// periksaDapatDiretur refuses a quantity larger than what is left of the goods that
// actually arrived on a line.
//
// What may go back is qty_diterima_dasar + Σ POSTED susulan − Σ POSTED retur. Note
// which quantity is absent: qty_dasar. The invoice quantity is what the supplier
// billed, and goods that never arrived cannot be sent back — a shortfall is chased
// with a follow-up receipt, not returned.
//
// The figure is read fresh rather than passed in, because it counts POSTED returns and
// those can appear between a draft being written and posted.
func (c *ReturPembelianUseCase) periksaDapatDiretur(ctx context.Context, tx repository.DBTX, idPembelian, idPembelianDetail, qtyDasar int64, nomorBaris int) error {
	asal, err := c.PembelianRepository.FindDetailByID(ctx, tx, idPembelianDetail)
	if err != nil {
		return notFoundOnNoRows(err, fmt.Sprintf("baris %d: baris pembelian tidak ditemukan", nomorBaris))
	}

	if asal.IDPembelian != idPembelian {
		return model.Invalid(fmt.Sprintf("baris %d: baris itu milik pembelian lain", nomorBaris))
	}

	dapat := asal.QtyDiterimaDasar + asal.QtySusulanDasar - asal.QtyReturDasar

	if dapat <= 0 {
		return model.Invalid(fmt.Sprintf(
			"baris %d: tidak ada barang yang bisa diretur dari baris pembelian itu", nomorBaris,
		))
	}

	if qtyDasar > dapat {
		return model.Invalid(fmt.Sprintf(
			"baris %d: qty %d satuan dasar melebihi yang bisa diretur, %d", nomorBaris, qtyDasar, dapat,
		))
	}

	return nil
}

// tulisDetail inserts the lines and refreshes the header's total.
func (c *ReturPembelianUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, id int64, detail []entity.ReturPembelianDetail) error {
	total := new(big.Rat)

	for i := range detail {
		if err := c.ReturRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return invalidOnForeignKey(
				conflictOnUnique(err, "baris pembelian yang sama dipakai dua kali"),
				"id_pembelian_detail atau id_satuan_input tidak ada",
			)
		}

		total.Add(total, mustParseNumeric(detail[i].Nilai))
	}

	return c.ReturRepository.RecalculateTotal(
		ctx, tx, id, formatNumeric(roundNumeric(total, skalaUang), skalaUang),
	)
}
