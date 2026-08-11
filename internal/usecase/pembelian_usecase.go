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

// PembelianUseCase owns the purchase document: its state machine, its costing
// arithmetic, and the transaction that writes it together with its stock movements.
//
// It holds four repositories because posting is one atomic act across four tables.
// The header, its lines, the reserved document number, and every kartu_stok row are
// written in a single transaction — a partial posting would leave stock that no
// document explains, and kartu_stok cannot be edited afterwards to fix it.
type PembelianUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	PembelianRepository *repository.PembelianRepository
	ProductRepository   *repository.ProductRepository
	KartuStokRepository *repository.KartuStokRepository
	CounterRepository   *repository.DocumentCounterRepository
}

func NewPembelianUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	pembelianRepository *repository.PembelianRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
) *PembelianUseCase {
	return &PembelianUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		PembelianRepository: pembelianRepository,
		ProductRepository:   productRepository,
		KartuStokRepository: kartuStokRepository,
		CounterRepository:   counterRepository,
	}
}

// Create opens a DRAFT with its lines.
//
// The document is always DRAFT: status is not in the request at all. Stock moves at
// posting and only at posting, and posting is a separate call with a different
// role behind it.
func (c *PembelianUseCase) Create(ctx context.Context, request *model.CreatePembelianRequest) (*model.PembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal, err := time.Parse(dateOnly, request.Tanggal)
	if err != nil {
		return nil, model.Invalid("tanggal harus YYYY-MM-DD")
	}

	tanggalFaktur, err := parseTanggalOpsional(request.TanggalFaktur, "tanggal_faktur")
	if err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	if request.NoFakturSupplier != nil && *request.NoFakturSupplier != "" {
		exists, err := c.PembelianRepository.ExistsFakturSupplier(
			ctx, tx, request.IDSupplier, *request.NoFakturSupplier, 0,
		)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("no faktur supplier itu sudah pernah diinput")
		}
	}

	nomor, err := c.nomorBaru(ctx, tx, repository.PrefixPembelian, tanggal)
	if err != nil {
		return nil, err
	}

	pembelian := &entity.Pembelian{
		Nomor:            nomor,
		Tanggal:          tanggal,
		IDSupplier:       request.IDSupplier,
		IDRuang:          request.IDRuang,
		NoFakturSupplier: request.NoFakturSupplier,
		TanggalFaktur:    tanggalFaktur,

		DiskonNota:     nilaiAtauNol(request.DiskonNota),
		PPN:            nilaiAtauNol(request.PPN),
		PPNDikreditkan: request.PPNDikreditkan,
		Pembulatan:     nilaiAtauNol(request.Pembulatan),

		IDEkspedisi:         request.IDEkspedisi,
		NoResi:              request.NoResi,
		TotalKoli:           request.TotalKoli,
		TarifPerKoli:        request.TarifPerKoli,
		DitanggungSupplier:  request.DitanggungSupplier,
		MetodeAlokasiAngkut: pilihanAtau(request.MetodeAlokasiAngkut, entity.MetodeAlokasiKoli),
		JenisPembayaran:     pilihanAtau(request.JenisPembayaran, entity.JenisPembayaranTunai),

		// Rebuilt from the lines a few statements below; sending the caller's idea
		// of the totals would let the header disagree with its own detail.
		Subtotal:    "0",
		Total:       "0",
		BiayaAngkut: "0",
		Status:      entity.StatusPembelianDraft,
		CreatedBy:   request.ActorID,
	}

	if err := c.PembelianRepository.Create(ctx, tx, pembelian); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "no faktur supplier itu sudah pernah diinput"),
			"id_supplier, id_ruang, atau id_ekspedisi tidak ada",
		)
	}

	detail, err := c.siapkanDetail(ctx, tx, pembelian.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	for i := range detail {
		if err := c.PembelianRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return nil, invalidOnForeignKey(err, "id_product atau id_satuan_input tidak ada")
		}
	}

	if err := c.sinkronkanHeader(ctx, tx, pembelian, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored row and its joined names rather
	// than what was sent.
	return c.detail(ctx, c.DB, pembelian.ID)
}

func (c *PembelianUseCase) Get(ctx context.Context, request *model.GetPembelianRequest) (*model.PembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PembelianUseCase) Search(ctx context.Context, request *model.ListPembelianRequest) ([]model.PembelianResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PembelianRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.StatusPenerimaan, request.IDSupplier,
		request.TanggalDari, request.TanggalSampai,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PembelianToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT and rebuilds the totals that depend on it.
func (c *PembelianUseCase) Update(ctx context.Context, request *model.UpdatePembelianRequest) (*model.PembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	patch, err := patchDariRequest(request)
	if err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	pembelian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDraft)
	if err != nil {
		return nil, err
	}

	if request.NoFakturSupplier.Set() {
		exists, err := c.PembelianRepository.ExistsFakturSupplier(
			ctx, tx, pembelian.IDSupplier, *request.NoFakturSupplier.Value, request.ID,
		)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("no faktur supplier itu sudah pernah diinput")
		}
	}

	if err := c.PembelianRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(
				notFoundOnNoRows(err, "pembelian not found"),
				"no faktur supplier itu sudah pernah diinput",
			),
			"id_ekspedisi tidak ada",
		)
	}

	// Re-read: diskon_nota, ppn, pembulatan, total_koli and tarif_per_koli all feed
	// the header totals, so the patched values are what has to drive the rebuild.
	pembelian, err = c.PembelianRepository.LockByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	detail, err := c.PembelianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if err := c.sinkronkanHeader(ctx, tx, pembelian, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceDetail swaps the whole line set of a DRAFT.
func (c *PembelianUseCase) ReplaceDetail(ctx context.Context, request *model.ReplacePembelianDetailRequest) (*model.PembelianResponse, error) {
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

	pembelian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDraft)
	if err != nil {
		return nil, err
	}

	// Safe only because the document is a DRAFT: a posted line is what
	// retur_pembelian_detail points at and the source of cost for every reversal,
	// so deleting one would erase the audit trail.
	if err := c.PembelianRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, err := c.siapkanDetail(ctx, tx, request.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	for i := range detail {
		if err := c.PembelianRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return nil, invalidOnForeignKey(err, "id_product atau id_satuan_input tidak ada")
		}
	}

	if err := c.sinkronkanHeader(ctx, tx, pembelian, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Ajukan hands a draft to the approver.
//
// This is the point the document stops being editable. Everything after it is the
// approver's decision, and the only ways out are posting or rejection.
func (c *PembelianUseCase) Ajukan(ctx context.Context, request *model.AjukanPembelianRequest) (*model.PembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDraft); err != nil {
		return nil, err
	}

	detail, err := c.PembelianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("pembelian tanpa baris tidak bisa diajukan")
	}

	if err := c.PembelianRepository.Ajukan(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Tolak sends a submission back to DRAFT so the operator can fix it.
func (c *PembelianUseCase) Tolak(ctx context.Context, request *model.TolakPembelianRequest) (*model.PembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDiajukan); err != nil {
		return nil, err
	}

	if err := c.PembelianRepository.Tolak(ctx, tx, request.ID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(err, "status pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Posting approves a submission: it allocates freight, computes cost per base unit,
// writes one kartu_stok row per line that actually received goods, and closes the
// document. All of it in one transaction.
//
// Only quantities that arrived reach kartu_stok, and only the matching share of the
// value goes with them. A line where nothing came is written to no stock card at
// all — a zero-quantity movement would be a row saying nothing happened.
func (c *PembelianUseCase) Posting(ctx context.Context, request *model.PostingPembelianRequest) (*model.PembelianResponse, error) {
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

	pembelian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDiajukan)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the
	// status column being right. Posting twice doubles the stock, and kartu_stok is
	// append-only — that cannot be corrected, only reversed.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTablePembelian, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("pembelian ini sudah punya baris kartu stok")
	}

	detail, err := c.PembelianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("pembelian tanpa baris tidak bisa diposting")
	}

	baris, err := c.siapkanPosting(pembelian, detail)
	if err != nil {
		return nil, err
	}

	for i := range detail {
		if err := c.PembelianRepository.UpdateDetailPosting(
			ctx, tx, detail[i].ID,
			formatNumeric(baris[i].AlokasiBiaya, skalaUang),
			formatNumeric(baris[i].HargaPokokSatuanDasar, skalaHPP),
		); err != nil {
			return nil, err
		}

		detail[i].AlokasiBiaya = formatNumeric(baris[i].AlokasiBiaya, skalaUang)

		if baris[i].QtyDiterimaDasar == 0 {
			continue
		}

		qtyInput := detail[i].QtyDiterima
		kartu := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          pembelian.IDRuang,
			TanggalTransaksi: pembelian.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiPembelian,
			StokMasuk:        baris[i].QtyDiterimaDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &detail[i].IDSatuanInput,
			NilaiMasuk:       formatNumeric(baris[i].NilaiMasuk, skalaUang),
			RefTable:         entity.RefTablePembelian,
			RefIDTransaksi:   pembelian.ID,
			CreatedBy:        request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				"posting ditolak kartu stok: periode sudah TUTUP atau saldo tidak mencukupi",
			)
		}
	}

	if err := c.sinkronkanHeader(ctx, tx, pembelian, detail); err != nil {
		return nil, err
	}

	if err := c.PembelianRepository.Posting(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted document by appending a reversing row for every movement it
// made. Nothing is deleted and nothing is edited — kartu_stok forbids both.
//
// One thing this cannot do, and it is worth knowing before reading the numbers
// back: the reversal is valued at the current moving average, not at the cost the
// original row carried. kartu_stok_hitung_saldo overwrites nilai_keluar and
// harga_pokok_satuan on every outgoing row with the balance in force at the time,
// so an application-supplied cost is discarded. When goods bought at one price were
// received, mixed with older stock, and partly sold before the cancellation, the
// reversal takes out the blended cost rather than the purchase cost. That is
// inherent to a moving average, not a shortcut here — undoing the mixing would need
// a different valuation method.
func (c *PembelianUseCase) Batal(ctx context.Context, request *model.BatalPembelianRequest) (*model.PembelianResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianPosted); err != nil {
		return nil, err
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTablePembelian, request.ID)
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
			// Mirrored: what came in goes out. Purchases only ever move stock in,
			// but writing both directions keeps this correct if a reversing path
			// ever reaches it from the other side.
			StokMasuk:       asal[i].StokKeluar,
			StokKeluar:      asal[i].StokMasuk,
			NilaiMasuk:      asal[i].NilaiKeluar,
			RefTable:        entity.RefTablePembelian,
			RefIDTransaksi:  request.ID,
			IDKartuStokAsal: &idAsal,
			Keterangan:      &keterangan,
			CreatedBy:       request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return nil, invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: barangnya sudah terpakai atau periode sudah TUTUP",
			)
		}
	}

	if err := c.PembelianRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status pembelian sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// BagiRataKoli spreads total_koli across the lines in proportion to qty_dasar.
//
// It exists because posting refuses a document whose koli do not add up, and typing
// a share per line by hand is exactly the kind of arithmetic that produces a
// document off by 0.01. The parts sum to exactly total_koli.
func (c *PembelianUseCase) BagiRataKoli(ctx context.Context, request *model.BagiRataKoliRequest) (*model.PembelianResponse, error) {
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

	pembelian, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembelianDraft)
	if err != nil {
		return nil, err
	}

	if pembelian.TotalKoli == nil {
		return nil, model.Invalid("total_koli belum diisi")
	}

	totalKoli := mustParseNumeric(*pembelian.TotalKoli)
	if totalKoli.Sign() <= 0 {
		return nil, model.Invalid("total_koli harus lebih dari nol")
	}

	detail, err := c.PembelianRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("pembelian tanpa baris tidak bisa dibagi rata")
	}

	basis := make([]*big.Rat, len(detail))
	for i := range detail {
		basis[i] = ratFromInt(detail[i].QtyDasar)
	}

	bagian := bagiProporsional(totalKoli, basis, skalaKoli)

	for i := range detail {
		if err := c.PembelianRepository.UpdateJumlahKoli(
			ctx, tx, detail[i].ID, formatNumeric(bagian[i], skalaKoli),
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Sisa lists the lines the supplier still owes.
func (c *PembelianUseCase) Sisa(ctx context.Context, request *model.GetPembelianRequest) (*model.SisaPembelianResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	pembelian, err := c.PembelianRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	pembelian.Detail, err = c.PembelianRepository.FindDetail(ctx, c.DB, request.ID)
	if err != nil {
		return nil, err
	}

	return converter.PembelianToSisaResponse(pembelian), nil
}

// detail loads a header and its lines. Two queries, independent of how many lines
// come back.
func (c *PembelianUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.PembelianResponse, error) {
	pembelian, err := c.PembelianRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the
	// key and implying the document was never asked about.
	pembelian.Detail, err = c.PembelianRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.PembelianToResponse(pembelian), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
//
// Locking before deciding is what makes the state machine safe: without it two
// concurrent posting requests both read DIAJUKAN and both proceed. The message
// names the state the caller found it in, because "conflict" alone leaves an
// operator with nothing to do next.
func (c *PembelianUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.Pembelian, error) {
	pembelian, err := c.PembelianRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembelian not found")
	}

	if pembelian.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, pembelian sekarang %s", diharapkan, pembelian.Status,
		))
	}

	return pembelian, nil
}

// nomorBaru reserves the next number in the series for the document's own month, so
// a document dated in July gets a July number however late it is typed in.
func (c *PembelianUseCase) nomorBaru(ctx context.Context, tx repository.DBTX, prefix string, tanggal time.Time) (string, error) {
	urut, err := c.CounterRepository.Next(ctx, tx, prefix, tanggal.Year(), int(tanggal.Month()))
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/%04d/%02d/%04d", prefix, tanggal.Year(), int(tanggal.Month()), urut), nil
}

// siapkanDetail turns request lines into rows: it resolves conversion factors,
// converts both quantities to base units, and computes each line's subtotal.
//
// Every check here is one the database cannot make. The factor lookup is a join the
// schema does not enforce (pembelian_detail.id_satuan_input references satuan, not
// product_satuan), the whole-number rule comes from qty_dasar being BIGINT, and the
// mandatory note on a short delivery is a policy, not a constraint.
func (c *PembelianUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idPembelian int64, requests []model.PembelianDetailRequest) ([]entity.PembelianDetail, error) {
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

	detail := make([]entity.PembelianDetail, 0, len(requests))

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

		qtyFaktur, err := parseNumeric(baris.QtyFaktur)
		if err != nil {
			return nil, err
		}
		if qtyFaktur.Sign() <= 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: qty_faktur harus lebih dari nol", i+1))
		}

		// Omitting qty_diterima means everything on the paper was in the box, which
		// is the ordinary case. Making the operator retype it would invite typos on
		// exactly the lines that are fine.
		qtyDiterima := new(big.Rat).Set(qtyFaktur)
		if baris.QtyDiterima != nil {
			qtyDiterima, err = parseNumeric(*baris.QtyDiterima)
			if err != nil {
				return nil, err
			}
		}

		if qtyDiterima.Sign() < 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: qty_diterima tidak boleh negatif", i+1))
		}

		// Over-delivery is refused. Goods that were never invoiced carry no value,
		// so the proportional nilai_masuk would exceed the invoice and the moving
		// average would be wrong from then on — permanently, since kartu_stok is
		// append-only.
		if qtyDiterima.Cmp(qtyFaktur) > 0 {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_diterima tidak boleh melebihi qty_faktur", i+1,
			))
		}

		selisih := qtyDiterima.Cmp(qtyFaktur) != 0
		if selisih && (baris.KeteranganSelisih == nil || *baris.KeteranganSelisih == "") {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: keterangan_selisih wajib diisi saat qty_diterima berbeda dari qty_faktur", i+1,
			))
		}

		konversiRat := ratFromInt(konversi)

		qtyDasar := new(big.Rat).Mul(qtyFaktur, konversiRat)
		if !isWholeNumber(qtyDasar) {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_faktur x faktor menghasilkan %s satuan dasar, bukan bilangan bulat",
				i+1, formatNumeric(qtyDasar, skalaQty),
			))
		}

		qtyDiterimaDasar := new(big.Rat).Mul(qtyDiterima, konversiRat)
		if !isWholeNumber(qtyDiterimaDasar) {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_diterima x faktor menghasilkan %s satuan dasar, bukan bilangan bulat",
				i+1, formatNumeric(qtyDiterimaDasar, skalaQty),
			))
		}

		harga, err := parseNumeric(baris.HargaSatuanInput)
		if err != nil {
			return nil, err
		}
		if harga.Sign() < 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: harga_satuan_input tidak boleh negatif", i+1))
		}

		diskonBaris, err := parseNumeric(nilaiAtauNol(baris.DiskonBaris))
		if err != nil {
			return nil, err
		}
		if diskonBaris.Sign() < 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: diskon_baris tidak boleh negatif", i+1))
		}

		jumlahKoli, err := parseNumeric(nilaiAtauNol(baris.JumlahKoli))
		if err != nil {
			return nil, err
		}
		if jumlahKoli.Sign() < 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: jumlah_koli tidak boleh negatif", i+1))
		}

		// Subtotal follows the invoice, not the delivery: the supplier is owed what
		// the paper says, and the shortfall is settled with them separately.
		subtotal := new(big.Rat).Mul(qtyFaktur, harga)
		subtotal.Sub(subtotal, diskonBaris)

		if subtotal.Sign() < 0 {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: diskon_baris melebihi nilai barisnya", i+1,
			))
		}

		detail = append(detail, entity.PembelianDetail{
			IDPembelian:       idPembelian,
			IDProduct:         baris.IDProduct,
			QtyFaktur:         formatNumeric(qtyFaktur, skalaQty),
			QtyDiterima:       formatNumeric(qtyDiterima, skalaQty),
			IDSatuanInput:     baris.IDSatuanInput,
			FaktorKonversi:    konversi,
			QtyDasar:          ratKeInt64(qtyDasar),
			QtyDiterimaDasar:  ratKeInt64(qtyDiterimaDasar),
			HargaSatuanInput:  formatNumeric(harga, skalaUang),
			DiskonBaris:       formatNumeric(diskonBaris, skalaUang),
			Subtotal:          formatNumeric(roundNumeric(subtotal, skalaUang), skalaUang),
			JumlahKoli:        formatNumeric(jumlahKoli, skalaKoli),
			KeteranganSelisih: baris.KeteranganSelisih,
		})
	}

	return detail, nil
}

// siapkanPosting runs the costing arithmetic and the checks the database cannot
// make, returning the computed lines in the same order as detail.
func (c *PembelianUseCase) siapkanPosting(pembelian *entity.Pembelian, detail []entity.PembelianDetail) ([]barisPosting, error) {
	biayaAngkut := hitungBiayaAngkut(pembelian)

	// The koli figures come off the carrier's note; the per-line shares are typed
	// by the warehouse while unpacking. If they do not add up, some freight would
	// be allocated to nobody and the inventory value would silently be short.
	// bagi-rata-koli exists so this never has to be reconciled by hand.
	if pembelian.MetodeAlokasiAngkut == entity.MetodeAlokasiKoli && biayaAngkut.Sign() > 0 && pembelian.TotalKoli != nil {
		totalKoli := mustParseNumeric(*pembelian.TotalKoli)

		if totalKoli.Sign() > 0 {
			jumlah := new(big.Rat)
			for i := range detail {
				jumlah.Add(jumlah, mustParseNumeric(detail[i].JumlahKoli))
			}

			if jumlah.Cmp(totalKoli) != 0 {
				return nil, model.Invalid(fmt.Sprintf(
					"jumlah_koli seluruh baris (%s) harus sama dengan total_koli (%s)",
					formatNumeric(jumlah, skalaKoli), formatNumeric(totalKoli, skalaKoli),
				))
			}
		}
	}

	subtotal := new(big.Rat)
	for i := range detail {
		subtotal.Add(subtotal, mustParseNumeric(detail[i].Subtotal))
	}

	diskonNota := mustParseNumeric(pembelian.DiskonNota)
	if diskonNota.Cmp(subtotal) > 0 {
		return nil, model.Invalid("diskon_nota melebihi subtotal; harga pokok akan negatif")
	}

	baris := make([]barisPosting, len(detail))
	for i := range detail {
		baris[i] = barisPosting{
			QtyDasar:         detail[i].QtyDasar,
			QtyDiterimaDasar: detail[i].QtyDiterimaDasar,
			Subtotal:         mustParseNumeric(detail[i].Subtotal),
			JumlahKoli:       mustParseNumeric(detail[i].JumlahKoli),
		}
	}

	hitungPosting(headerPosting{
		DiskonNota:     diskonNota,
		PPN:            mustParseNumeric(pembelian.PPN),
		PPNDikreditkan: pembelian.PPNDikreditkan,
		BiayaAngkut:    biayaAngkut,
		Metode:         pembelian.MetodeAlokasiAngkut,
	}, baris)

	return baris, nil
}

// sinkronkanHeader rebuilds every header figure that is derived from somewhere
// else: the invoice totals from the lines, the freight bill from the koli figures,
// and the receipt cache from the two quantity columns.
//
// status_penerimaan follows the same rule as status_pembayaran — always recomputed,
// never set from a form. A cache a form can write is not a cache, it is a second
// source of truth.
func (c *PembelianUseCase) sinkronkanHeader(ctx context.Context, tx repository.DBTX, pembelian *entity.Pembelian, detail []entity.PembelianDetail) error {
	subtotal := new(big.Rat)
	statusPenerimaan := entity.StatusPenerimaanLengkap

	for i := range detail {
		subtotal.Add(subtotal, mustParseNumeric(detail[i].Subtotal))

		if detail[i].QtyDiterimaDasar != detail[i].QtyDasar {
			statusPenerimaan = entity.StatusPenerimaanKurang
		}
	}

	diskonNota := mustParseNumeric(pembelian.DiskonNota)
	if diskonNota.Cmp(subtotal) > 0 {
		return model.Invalid("diskon_nota melebihi subtotal")
	}

	// Total is what the supplier is owed. biaya_angkut is deliberately absent: it
	// is the carrier's bill, and it reaches the books as alokasi_biaya on the lines
	// instead. Adding it here would inflate the payable by money owed elsewhere.
	total := new(big.Rat).Set(subtotal)
	total.Sub(total, diskonNota)
	total.Add(total, mustParseNumeric(pembelian.PPN))
	total.Add(total, mustParseNumeric(pembelian.Pembulatan))

	return c.PembelianRepository.RecalculateHeader(
		ctx, tx, pembelian.ID,
		formatNumeric(roundNumeric(subtotal, skalaUang), skalaUang),
		formatNumeric(roundNumeric(total, skalaUang), skalaUang),
		formatNumeric(hitungBiayaAngkut(pembelian), skalaUang),
		statusPenerimaan,
	)
}

// hitungBiayaAngkut derives the carrier's bill from the two figures on their note:
// biaya_angkut = total_koli x tarif_per_koli.
//
// ditanggung_supplier zeroes it, and that is the whole point of the flag. Freight
// already inside the supplier's invoice is part of the line prices; allocating it
// again would count the same rupiah into cost of goods twice.
func hitungBiayaAngkut(pembelian *entity.Pembelian) *big.Rat {
	if pembelian.DitanggungSupplier || pembelian.TotalKoli == nil || pembelian.TarifPerKoli == nil {
		return new(big.Rat)
	}

	biaya := new(big.Rat).Mul(
		mustParseNumeric(*pembelian.TotalKoli),
		mustParseNumeric(*pembelian.TarifPerKoli),
	)

	return roundNumeric(biaya, skalaUang)
}

// patchDariRequest maps the DTO's Optional fields onto the repository patch, and
// rejects an explicit null on a NOT NULL column.
//
// COALESCE would be correct for those columns, but silently: `{"ppn": null}` would
// read as "leave it alone" and the caller would never learn their request did
// nothing.
func patchDariRequest(request *model.UpdatePembelianRequest) (repository.PembelianPatch, error) {
	var patch repository.PembelianPatch

	for nama, wajib := range map[string]bool{
		"tanggal":               request.Tanggal.Clears(),
		"diskon_nota":           request.DiskonNota.Clears(),
		"ppn":                   request.PPN.Clears(),
		"ppn_dikreditkan":       request.PPNDikreditkan.Clears(),
		"pembulatan":            request.Pembulatan.Clears(),
		"ditanggung_supplier":   request.DitanggungSupplier.Clears(),
		"metode_alokasi_angkut": request.MetodeAlokasiAngkut.Clears(),
		"jenis_pembayaran":      request.JenisPembayaran.Clears(),
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

	if request.TanggalFaktur.Present {
		patch.SetTanggalFaktur = true

		if request.TanggalFaktur.Value != nil {
			tanggal, err := time.Parse(dateOnly, *request.TanggalFaktur.Value)
			if err != nil {
				return patch, model.Invalid("tanggal_faktur harus YYYY-MM-DD")
			}

			patch.TanggalFaktur = &tanggal
		}
	}

	patch.SetNoFakturSupplier = request.NoFakturSupplier.Present
	patch.NoFakturSupplier = request.NoFakturSupplier.Value
	patch.DiskonNota = request.DiskonNota.Value
	patch.PPN = request.PPN.Value
	patch.PPNDikreditkan = request.PPNDikreditkan.Value
	patch.Pembulatan = request.Pembulatan.Value
	patch.SetIDEkspedisi = request.IDEkspedisi.Present
	patch.IDEkspedisi = request.IDEkspedisi.Value
	patch.SetNoResi = request.NoResi.Present
	patch.NoResi = request.NoResi.Value
	patch.SetTotalKoli = request.TotalKoli.Present
	patch.TotalKoli = request.TotalKoli.Value
	patch.SetTarifPerKoli = request.TarifPerKoli.Present
	patch.TarifPerKoli = request.TarifPerKoli.Value
	patch.DitanggungSupplier = request.DitanggungSupplier.Value
	patch.MetodeAlokasiAngkut = request.MetodeAlokasiAngkut.Value
	patch.JenisPembayaran = request.JenisPembayaran.Value

	return patch, nil
}

// parseTanggalOpsional converts an optional YYYY-MM-DD field. The datetime tag
// already proved the layout; this turns it into a time.
func parseTanggalOpsional(text *string, nama string) (*time.Time, error) {
	if text == nil || *text == "" {
		return nil, nil
	}

	tanggal, err := time.Parse(dateOnly, *text)
	if err != nil {
		return nil, model.Invalid(nama + " harus YYYY-MM-DD")
	}

	return &tanggal, nil
}

// nilaiAtauNol treats an omitted money field as zero, so a caller may leave out
// every line they have nothing to say about.
func nilaiAtauNol(text string) string {
	if text == "" {
		return "0"
	}

	return text
}

// pilihanAtau applies the schema default when the caller omitted an enum field, so
// the value stored is the one this code reasons about rather than whatever the
// column happens to default to.
func pilihanAtau(text, standar string) string {
	if text == "" {
		return standar
	}

	return text
}

// ratKeInt64 narrows a rational already proved whole by isWholeNumber.
func ratKeInt64(value *big.Rat) int64 {
	return value.Num().Int64()
}
