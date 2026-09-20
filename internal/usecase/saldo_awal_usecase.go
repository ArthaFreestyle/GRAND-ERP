package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// SaldoAwalUseCase owns the opening-stock document — isu #43. It is the eighth
// writer of kartu_stok: stock entering a room with no origin, at a cost the operator
// TYPES.
//
// That last part is the whole reason it is dangerous. Everywhere else the cost is
// derived (pembelian), copied from a source document (penerimaan_susulan,
// retur_pembelian), read back from RETURNING (mutasi, pemakaian, penjualan) or taken
// from the room's own average (stok_opname surplus). A migration is the one situation
// with no source document, so a typed cost is unavoidable — and what stops this from
// being a back door for injecting inventory value is not the workflow but the fence:
//
//   - One (barang, ruang) for life, no exceptions. Posting is refused 409 when
//     kartu_stok already holds ANY row for the pair — a purchase's, a transfer's, an
//     earlier saldo_awal's, and a reversing row too. See Posting.
//   - The full approval flow (DRAFT -> DIAJUKAN -> POSTED -> BATAL) stays, unlike
//     mutasi and penjualan. mutasi drops the stage because its mistake is cheap —
//     total stock and total value do not move — and that reasoning is exactly reversed
//     here: this document CREATES value from nothing.
//
// The consequence to state plainly: a mistyped saldo_awal cannot be redone. Cancelling
// it appends a reversing row (kartu_stok is append-only), which makes the pair have
// history, which closes the door for good. That is not a side effect to fix — it is
// the fence working. The correction is stok_opname, which from that point on has an
// id_kartu_stok_cutoff to point at and a moving average that is defined.
type SaldoAwalUseCase struct {
	DB                   *sql.DB
	Log                  *logrus.Logger
	Validate             *validator.Validate
	SaldoAwalRepository  *repository.SaldoAwalRepository
	ProductRepository    *repository.ProductRepository
	KartuStokRepository  *repository.KartuStokRepository
	CounterRepository    *repository.DocumentCounterRepository
	PeriodeRepository    *repository.PeriodeRepository
	RuangRepository      *repository.RuangRepository
	StokOpnameRepository *repository.StokOpnameRepository
	UnitKerjaRepository  *repository.UnitKerjaRepository
}

func NewSaldoAwalUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	saldoAwalRepository *repository.SaldoAwalRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
	periodeRepository *repository.PeriodeRepository,
	ruangRepository *repository.RuangRepository,
	stokOpnameRepository *repository.StokOpnameRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *SaldoAwalUseCase {
	return &SaldoAwalUseCase{
		DB:                   db,
		Log:                  log,
		Validate:             validate,
		SaldoAwalRepository:  saldoAwalRepository,
		ProductRepository:    productRepository,
		KartuStokRepository:  kartuStokRepository,
		CounterRepository:    counterRepository,
		PeriodeRepository:    periodeRepository,
		RuangRepository:      ruangRepository,
		StokOpnameRepository: stokOpnameRepository,
		UnitKerjaRepository:  unitKerjaRepository,
	}
}

// Create opens a DRAFT with its lines. Lines are optional here and mandatory on
// ReplaceDetail; submitting refuses a document with no lines anyway.
func (c *SaldoAwalUseCase) Create(ctx context.Context, request *model.CreateSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if strings.TrimSpace(request.Alasan) == "" {
		return nil, model.Invalid("alasan tidak boleh kosong")
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

	if err := periksaRuangUnitAktif(ctx, tx, c.RuangRepository, request.AktifIDUnitKerja, request.IDRuang); err != nil {
		return nil, err
	}

	// Friendlier message sooner; Posting repeats it, since the catalog can change in between.
	if err := periksaKatalogRuang(ctx, tx, c.RuangRepository, c.ProductRepository, request.IDRuang,
		idProductBaris(request.Detail, func(b *model.SaldoAwalDetailRequest) int64 { return b.IDProduct })); err != nil {
		return nil, err
	}

	nomor, err := nomorDokumenUntukRuang(
		ctx, tx, c.CounterRepository, c.RuangRepository, c.UnitKerjaRepository,
		repository.PrefixSaldoAwal, tanggal, request.IDRuang,
	)
	if err != nil {
		return nil, err
	}

	saldoAwal := &entity.SaldoAwal{
		Nomor:     nomor,
		Tanggal:   tanggal,
		IDRuang:   request.IDRuang,
		Alasan:    request.Alasan,
		Status:    entity.StatusSaldoAwalDraft,
		CreatedBy: request.ActorID,
	}

	if err := c.SaldoAwalRepository.Create(ctx, tx, saldoAwal); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "nomor dokumen sudah dipakai"),
			"id_ruang tidak ada",
		)
	}

	detail, err := c.siapkanDetail(ctx, tx, saldoAwal.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := c.periksaBelumPernahBergerak(ctx, tx, request.IDRuang, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, saldoAwal.ID, nil)
}

func (c *SaldoAwalUseCase) Get(ctx context.Context, request *model.GetSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, request.AktifIDUnitKerja)
}

func (c *SaldoAwalUseCase) Search(ctx context.Context, request *model.ListSaldoAwalRequest) ([]model.SaldoAwalResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.SaldoAwalRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDRuang,
		request.TanggalDari, request.TanggalSampai, request.AktifIDUnitKerja,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.SaldoAwalToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT. All three fields are NOT NULL columns:
// changeable, never clearable — alasan above all, the only record of why inventory
// value was created without a document behind it.
func (c *SaldoAwalUseCase) Update(ctx context.Context, request *model.UpdateSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if request.Tanggal.Clears() {
		return nil, model.Invalid("tanggal cannot be null")
	}
	if request.IDRuang.Clears() {
		return nil, model.Invalid("id_ruang cannot be null")
	}
	if request.Alasan.Clears() {
		return nil, model.Invalid("alasan cannot be null")
	}

	var patch repository.SaldoAwalPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	if request.Alasan.Set() && strings.TrimSpace(*request.Alasan.Value) == "" {
		return nil, model.Invalid("alasan tidak boleh kosong")
	}

	patch.IDRuang = request.IDRuang.Value
	patch.Alasan = request.Alasan.Value

	if patch.Tanggal == nil && patch.IDRuang == nil && patch.Alasan == nil {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSaldoAwalDraft); err != nil {
		return nil, err
	}

	// Only when id_ruang is actually moving, and only the new value: a room never
	// changes its unit_kerja, so a stored id_ruang the patch leaves alone was already
	// valid.
	if patch.IDRuang != nil {
		if err := periksaRuangUnitAktif(ctx, tx, c.RuangRepository, request.AktifIDUnitKerja, *patch.IDRuang); err != nil {
			return nil, err
		}

		baris, err := c.SaldoAwalRepository.FindDetail(ctx, tx, request.ID)
		if err != nil {
			return nil, err
		}

		if err := periksaKatalogRuang(ctx, tx, c.RuangRepository, c.ProductRepository, *patch.IDRuang,
			idProductBaris(baris, func(b *entity.SaldoAwalDetail) int64 { return b.IDProduct })); err != nil {
			return nil, err
		}

		if err := c.periksaBelumPernahBergerak(ctx, tx, *patch.IDRuang, baris); err != nil {
			return nil, err
		}
	}

	if err := c.SaldoAwalRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnForeignKey(
			notFoundOnNoRows(err, "saldo_awal not found"),
			"id_ruang tidak ada",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// ReplaceDetail swaps the whole line set of a DRAFT — also how 500 SKUs are typed at
// once, which is why bulk import is not this module's job.
func (c *SaldoAwalUseCase) ReplaceDetail(ctx context.Context, request *model.ReplaceSaldoAwalDetailRequest) (*model.SaldoAwalResponse, error) {
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

	draft, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSaldoAwalDraft)
	if err != nil {
		return nil, err
	}

	if err := periksaKatalogRuang(ctx, tx, c.RuangRepository, c.ProductRepository, draft.IDRuang,
		idProductBaris(request.Detail, func(b *model.SaldoAwalDetailRequest) int64 { return b.IDProduct })); err != nil {
		return nil, err
	}

	if err := c.SaldoAwalRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	detail, err := c.siapkanDetail(ctx, tx, request.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	// Friendlier message sooner. It decides nothing: two drafts may both claim the same
	// pair, since a draft is not a posting — Posting is the check that counts.
	if err := c.periksaBelumPernahBergerak(ctx, tx, draft.IDRuang, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Ajukan hands a draft to the approver. This is the point the document stops being
// editable.
func (c *SaldoAwalUseCase) Ajukan(ctx context.Context, request *model.AjukanSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSaldoAwalDraft); err != nil {
		return nil, err
	}

	detail, err := c.SaldoAwalRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("saldo_awal tanpa baris tidak bisa diajukan")
	}

	if err := c.SaldoAwalRepository.Ajukan(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status saldo_awal sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Tolak sends a submission back to DRAFT with a reason, so the operator can fix it.
// Following pembelian rather than pemakaian: a rejection here means "recount, the
// figures do not add up" — a paper correction, not a business decision.
func (c *SaldoAwalUseCase) Tolak(ctx context.Context, request *model.TolakSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSaldoAwalDiajukan); err != nil {
		return nil, err
	}

	if err := c.SaldoAwalRepository.Tolak(ctx, tx, request.ID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(err, "status saldo_awal sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Posting approves a submission and writes one incoming kartu_stok row per line.
//
// The order of the first steps is the one every writer in the project shares, and it
// matters more here than anywhere: periode -> ruang (shared) -> balance locks, and only
// THEN the fence. Reading kartu_stok's history before the pair locks are held answers a
// question about a past that can still change before the first row is written — two
// documents claiming the same pair would both read "no history" and both write.
//
// The rows are dated the document's tanggal, not now(): the cutover date is a fact
// about the shelf at that instant, the same argument ts_cutoff makes for stok_opname.
// Posting is therefore refused when the period containing tanggal is TUTUP.
func (c *SaldoAwalUseCase) Posting(ctx context.Context, request *model.PostingSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
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

	saldoAwal, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusSaldoAwalDiajukan)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the status
	// column being right. Posting twice doubles the stock, and kartu_stok is
	// append-only — that can only be reversed, never repaired.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTableSaldoAwal, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("saldo_awal ini sudah punya baris kartu stok")
	}

	detail, err := c.SaldoAwalRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("saldo_awal tanpa baris tidak bisa diposting")
	}

	// A product can leave a catalog between typing a draft and posting it. Reads the
	// membership FOR SHARE, so a removal racing this posting waits for it.
	if err := periksaKatalogRuang(ctx, tx, c.RuangRepository, c.ProductRepository, saldoAwal.IDRuang,
		idProductBaris(detail, func(b *entity.SaldoAwalDetail) int64 { return b.IDProduct })); err != nil {
		return nil, err
	}

	if err := c.kunciJalurStok(ctx, tx, saldoAwal.IDRuang, detail, saldoAwal.Tanggal); err != nil {
		return nil, err
	}

	// Checked so the refusals can name their cause; the trigger stays the guard.
	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, saldoAwal.Tanggal); err != nil {
		return nil, err
	}

	if err := periksaRuangBeku(ctx, tx, c.StokOpnameRepository, saldoAwal.IDRuang); err != nil {
		return nil, err
	}

	// THE fence, and the check that counts — read with every balance lock held.
	if err := c.periksaBelumPernahBergerak(ctx, tx, saldoAwal.IDRuang, detail); err != nil {
		return nil, err
	}

	totalNilai := new(big.Rat)

	for i := range detail {
		qtyInput := detail[i].QtyInput
		satuanInput := detail[i].IDSatuanInput

		kartu := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          saldoAwal.IDRuang,
			TanggalTransaksi: saldoAwal.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiSaldoAwal,
			StokMasuk:        detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// The typed value, as it stands on the line — never re-derived from
			// harga_pokok_satuan_dasar, so the division cannot lose a cent.
			NilaiMasuk:     detail[i].NilaiMasuk,
			RefTable:       entity.RefTableSaldoAwal,
			RefIDTransaksi: saldoAwal.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				fmt.Sprintf(
					"posting ditolak kartu stok: periode %04d-%02d sudah TUTUP",
					saldoAwal.Tanggal.Year(), int(saldoAwal.Tanggal.Month()),
				),
			)
		}

		if err := c.SaldoAwalRepository.UpdateDetailKartuStok(ctx, tx, detail[i].ID, kartu.ID); err != nil {
			return nil, err
		}

		totalNilai.Add(totalNilai, mustParseNumeric(detail[i].NilaiMasuk))
	}

	if err := c.SaldoAwalRepository.RecalculateTotalNilai(
		ctx, tx, request.ID, formatNumeric(roundNumeric(totalNilai, skalaUang), skalaUang),
	); err != nil {
		return nil, err
	}

	if err := c.SaldoAwalRepository.Posting(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status saldo_awal sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Batal voids a document from any non-BATAL status.
//
// From DRAFT/DIAJUKAN no kartu_stok row was ever written, so it is a pure status
// change. From POSTED it appends a reversing row for every movement — and that is the
// moment the pair acquires history: the fence in Posting reads every row, reversals
// included, so the pair can never be given an opening balance again. Deliberate, not a
// side effect to repair; the way to correct a mistyped opening balance is stok_opname.
//
// Quantity always balances on the way back, value does not always: the outgoing leg is
// valued by the trigger at the room's CURRENT moving average, and between posting and
// cancelling the opening goods have blended into it.
//
// It can also be refused outright when the goods have since left the room — the same
// leg a stok_opname surplus cancellation has. The remedy is the pemakaian, penjualan
// or mutasi that records where they went, not forcing the cancellation.
func (c *SaldoAwalUseCase) Batal(ctx context.Context, request *model.BatalSaldoAwalRequest) (*model.SaldoAwalResponse, error) {
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

	saldoAwal, err := c.SaldoAwalRepository.LockByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "saldo_awal not found")
	}

	if saldoAwal.Status == entity.StatusSaldoAwalBatal {
		return nil, model.Conflict("saldo_awal ini sudah BATAL")
	}

	if saldoAwal.Status == entity.StatusSaldoAwalPosted {
		if err := c.balikkan(ctx, tx, saldoAwal, request); err != nil {
			return nil, err
		}
	}

	if err := c.SaldoAwalRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status saldo_awal sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// balikkan appends the reversing rows for a POSTED document, dated today like every
// reversal in this codebase — so a document whose period has since closed can still be
// voided, into the current period.
func (c *SaldoAwalUseCase) balikkan(ctx context.Context, tx repository.DBTX, saldoAwal *entity.SaldoAwal, request *model.BatalSaldoAwalRequest) error {
	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTableSaldoAwal, request.ID)
	if err != nil {
		return err
	}

	tanggalPembalik := time.Now()

	if err := c.kunciJalurPembalik(ctx, tx, saldoAwal.IDRuang, asal, tanggalPembalik); err != nil {
		return err
	}

	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, tanggalPembalik); err != nil {
		return err
	}

	if err := periksaRuangBeku(ctx, tx, c.StokOpnameRepository, saldoAwal.IDRuang); err != nil {
		return err
	}

	if err := c.periksaBarangMasihAda(ctx, tx, saldoAwal, asal); err != nil {
		return err
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
			TanggalTransaksi: tanggalPembalik,
			JenisTransaksi:   entity.JenisTransaksiPembatalanTransaksi,
			StokMasuk:        asal[i].StokKeluar,
			StokKeluar:       asal[i].StokMasuk,
			// An explicit "0", not "": the column is NOT NULL and "" is not a NUMERIC.
			// This is an outgoing row, so the trigger prices what leaves.
			NilaiMasuk:      "0",
			RefTable:        entity.RefTableSaldoAwal,
			RefIDTransaksi:  request.ID,
			IDKartuStokAsal: &idAsal,
			Keterangan:      &keterangan,
			CreatedBy:       request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: barang sudah keluar dari ruang atau periode sudah TUTUP",
			)
		}
	}

	return nil
}

// periksaBelumPernahBergerak is the fence: every product on the document must have NO
// kartu_stok row at all in the document's room. See KartuStokRepository.PernahBergerak.
//
// Called twice, following the quota pattern of penerimaan_susulan: at Create /
// ReplaceDetail purely for a friendlier and earlier message, and again at Posting with
// every balance lock held — the one that decides. Two drafts may both claim the same
// pair; a draft is not a posting.
//
// Answers 409, written by hand rather than through conflictOnUnique: the request is
// not malformed, the goods simply already have a history in that room. The message
// names every offender at once, the kode_barang and the room, and points at
// stok_opname as the honest way forward.
func (c *SaldoAwalUseCase) periksaBelumPernahBergerak(ctx context.Context, db repository.DBTX, idRuang int64, baris []entity.SaldoAwalDetail) error {
	if len(baris) == 0 {
		return nil
	}

	bergerak, err := c.KartuStokRepository.PernahBergerak(
		ctx, db,
		idProductBaris(baris, func(b *entity.SaldoAwalDetail) int64 { return b.IDProduct }),
		idRuang,
	)
	if err != nil {
		return err
	}

	if len(bergerak) == 0 {
		return nil
	}

	kode := make(map[int64]string, len(baris))
	for i := range baris {
		kode[baris[i].IDProduct] = baris[i].KodeBarang
	}

	nama := make([]string, 0, len(bergerak))
	for _, id := range bergerak {
		if k := kode[id]; k != "" {
			nama = append(nama, k)
		} else {
			nama = append(nama, fmt.Sprintf("#%d", id))
		}
	}

	return model.Conflict(fmt.Sprintf(
		"barang %s sudah punya riwayat kartu stok di ruang %s; saldo awal hanya untuk barang yang belum pernah bergerak di ruang itu — gunakan stok_opname",
		strings.Join(nama, ", "), c.namaRuang(ctx, db, idRuang),
	))
}

// namaRuang is for messages only: a room that cannot be read falls back to its id
// rather than turning a helpful refusal into an error.
func (c *SaldoAwalUseCase) namaRuang(ctx context.Context, db repository.DBTX, idRuang int64) string {
	ruang, err := c.RuangRepository.FindByID(ctx, db, idRuang)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			c.Log.WithError(err).Warn("saldo_awal: cannot read ruang name for a message")
		}

		return fmt.Sprintf("#%d", idRuang)
	}

	return ruang.NamaRuang
}

// periksaBarangMasihAda refuses a cancellation whose goods have since left the room,
// before the trigger has to — with a message naming the product. Not the guard: the
// trigger's negative-stock check is, and it decides under the advisory lock. It runs
// after kunciJalurPembalik, so what it reads cannot move underneath it.
func (c *SaldoAwalUseCase) periksaBarangMasihAda(ctx context.Context, tx repository.DBTX, saldoAwal *entity.SaldoAwal, asal []entity.KartuStok) error {
	barangIDs := make([]int64, 0, len(asal))
	ruangIDs := make([]int64, 0, len(asal))

	for i := range asal {
		barangIDs = append(barangIDs, asal[i].IDBarang)
		ruangIDs = append(ruangIDs, asal[i].IDRuang)
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, tx, barangIDs, ruangIDs)
	if err != nil {
		return err
	}

	baris, err := c.SaldoAwalRepository.FindDetail(ctx, tx, saldoAwal.ID)
	if err != nil {
		return err
	}

	kode := make(map[int64]string, len(baris))
	for i := range baris {
		kode[baris[i].IDProduct] = baris[i].KodeBarang
	}

	for i := range asal {
		if asal[i].IDKartuStokAsal != nil {
			continue
		}

		tersedia := saldo[repository.SaldoKey{IDBarang: asal[i].IDBarang, IDRuang: asal[i].IDRuang}].StokAkhir

		if tersedia < asal[i].StokMasuk {
			return model.Invalid(fmt.Sprintf(
				"pembatalan ditolak: barang %s sudah keluar dari ruang %s (saldo %d, saldo awal %d); catat pengeluarannya dengan dokumen yang sesuai",
				kode[asal[i].IDBarang], c.namaRuang(ctx, tx, saldoAwal.IDRuang), tersedia, asal[i].StokMasuk,
			))
		}
	}

	return nil
}

// kunciJalurStok takes every balance lock Posting's inserts will need, up front, in
// the canonical order every writer in the system uses — periode first, then ruang
// (shared), then (id_barang, id_ruang). The trigger takes one advisory lock per insert
// rather than one per document, so taking every key up front, sorted, is what makes an
// ABBA between two documents naming the same products impossible rather than unlikely.
func (c *SaldoAwalUseCase) kunciJalurStok(ctx context.Context, tx repository.DBTX, idRuang int64, baris []entity.SaldoAwalDetail, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	// isu #15: taken before KunciSaldo, so the frozen status periksaRuangBeku reads
	// afterwards cannot move underneath the rest of the transaction.
	if err := c.RuangRepository.LockShared(ctx, tx, idRuang); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(baris))
	for i := range baris {
		keys = append(keys, repository.SaldoKey{IDBarang: baris[i].IDProduct, IDRuang: idRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// kunciJalurPembalik is kunciJalurStok for a cancellation, where the pairs to lock are
// read off the rows being reversed rather than off the document's lines.
func (c *SaldoAwalUseCase) kunciJalurPembalik(ctx context.Context, tx repository.DBTX, idRuang int64, asal []entity.KartuStok, tanggal time.Time) error {
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

// detail loads a header and its lines. Two queries, independent of how many lines
// come back.
//
// aktifIDUnitKerja scopes the read — isu #12 fase 6 — and is threaded as a parameter
// rather than read from ambient state: Get passes the caller's real active unit, but
// every write-path call passes nil, since a caller who just acted on a document is by
// construction allowed to see the response their own action produced.
func (c *SaldoAwalUseCase) detail(ctx context.Context, db repository.DBTX, id int64, aktifIDUnitKerja *int64) (*model.SaldoAwalResponse, error) {
	saldoAwal, err := c.SaldoAwalRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "saldo_awal not found")
	}

	if diLuarUnitAktif(saldoAwal.IDUnitKerjaRuang, aktifIDUnitKerja) {
		return nil, model.NotFound("saldo_awal not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the key.
	saldoAwal.Detail, err = c.SaldoAwalRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.SaldoAwalToResponse(saldoAwal), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *SaldoAwalUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.SaldoAwal, error) {
	saldoAwal, err := c.SaldoAwalRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "saldo_awal not found")
	}

	if saldoAwal.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, saldo_awal sekarang %s", diharapkan, saldoAwal.Status,
		))
	}

	return saldoAwal, nil
}

// siapkanDetail turns request lines into rows: it resolves the conversion factor,
// converts the quantity to base units and computes the value entering stock.
//
// nilai_masuk = qty_input × harga_satuan_input, from the two figures as typed and
// rounded once — the value does not pass through harga / faktor, which is derived only
// so the line can be read per base unit. All of it in big.Rat, never float64.
//
// The same product on two lines is refused here so the message names the lines; the
// unique index is the backstop.
func (c *SaldoAwalUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idSaldoAwal int64, requests []model.SaldoAwalDetailRequest) ([]entity.SaldoAwalDetail, error) {
	if len(requests) == 0 {
		return nil, nil
	}

	productIDs := make([]int64, len(requests))
	satuanIDs := make([]int64, len(requests))
	pertama := make(map[int64]int, len(requests))

	for i := range requests {
		productIDs[i] = requests[i].IDProduct
		satuanIDs[i] = requests[i].IDSatuanInput

		if j, ada := pertama[requests[i].IDProduct]; ada {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: produk yang sama sudah ada di baris %d; saldo awal hanya boleh satu baris per produk",
				i+1, j+1,
			))
		}

		pertama[requests[i].IDProduct] = i
	}

	faktor, err := c.ProductRepository.FindFaktorBatch(ctx, tx, productIDs, satuanIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]entity.SaldoAwalDetail, 0, len(requests))

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
		if roundNumeric(qtyInput, skalaQty).Cmp(qtyInput) != 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: qty_input maksimal %d desimal", i+1, skalaQty))
		}

		harga, err := parseNumeric(baris.HargaSatuanInput)
		if err != nil {
			return nil, err
		}
		// Zero is refused on purpose: a free item would drag the room's moving average
		// down permanently, in the one document whose value is typed.
		if harga.Sign() <= 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: harga_satuan_input harus lebih dari nol", i+1))
		}
		// The value is computed from the price AS TYPED; a price the column would
		// silently round would make the stored figures disagree with each other.
		if roundNumeric(harga, skalaUang).Cmp(harga) != 0 {
			return nil, model.Invalid(fmt.Sprintf("baris %d: harga_satuan_input maksimal %d desimal", i+1, skalaUang))
		}

		qtyDasar := new(big.Rat).Mul(qtyInput, ratFromInt(konversi))
		if !isWholeNumber(qtyDasar) {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_input x faktor menghasilkan %s satuan dasar, bukan bilangan bulat",
				i+1, formatNumeric(qtyDasar, skalaQty),
			))
		}

		nilai := roundNumeric(new(big.Rat).Mul(qtyInput, harga), skalaUang)
		if nilai.Sign() <= 0 {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: qty_input x harga_satuan_input membulat ke nol", i+1,
			))
		}

		hargaPokok := new(big.Rat).Quo(harga, ratFromInt(konversi))

		rows = append(rows, entity.SaldoAwalDetail{
			IDSaldoAwal:           idSaldoAwal,
			IDProduct:             baris.IDProduct,
			QtyInput:              formatNumeric(qtyInput, skalaQty),
			IDSatuanInput:         baris.IDSatuanInput,
			FaktorKonversi:        konversi,
			QtyDasar:              ratKeInt64(qtyDasar),
			HargaSatuanInput:      formatNumeric(harga, skalaUang),
			HargaPokokSatuanDasar: formatNumeric(roundNumeric(hargaPokok, skalaHPP), skalaHPP),
			NilaiMasuk:            formatNumeric(nilai, skalaUang),
		})
	}

	return rows, nil
}

// tulisDetail inserts the lines. It re-reads them with their joined names afterwards,
// so the fence's message can name kode_barang even inside Create / ReplaceDetail.
func (c *SaldoAwalUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, detail []entity.SaldoAwalDetail) error {
	for i := range detail {
		if err := c.SaldoAwalRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return conflictOnUnique(
				invalidOnForeignKey(err, "id_product atau id_satuan_input tidak ada"),
				"produk yang sama muncul dua kali di dokumen ini",
			)
		}
	}

	if len(detail) == 0 {
		return nil
	}

	// Copy the joined names onto the rows just written; InsertDetail returns only the id.
	baru, err := c.SaldoAwalRepository.FindDetail(ctx, tx, detail[0].IDSaldoAwal)
	if err != nil {
		return err
	}

	nama := make(map[int64]entity.SaldoAwalDetail, len(baru))
	for i := range baru {
		nama[baru[i].IDProduct] = baru[i]
	}

	for i := range detail {
		detail[i].KodeBarang = nama[detail[i].IDProduct].KodeBarang
		detail[i].NamaProduct = nama[detail[i].IDProduct].NamaProduct
	}

	return nil
}
