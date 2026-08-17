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

// PembayaranUtangUseCase owns money paid to suppliers: the document, its allocation
// across invoices, and the payment-status caches that follow from it.
//
// This is the first module that touches no stock at all, and that shapes everything about
// it. There is no DIAJUKAN state, because the reason pembelian has one — kartu_stok is
// append-only, so a wrong posting can only be reversed and the reversal is mis-valued —
// simply does not apply. An allocation can be voided and every cache recomputed exactly.
//
// What replaces that safety is arithmetic that has to hold across three documents at once,
// and the rules are not symmetric:
//
//   - a payment may be allocated at most up to its own amount, and **less is normal** —
//     the remainder sits as a credit with the supplier
//   - an invoice may receive at most what it still owes, counting POSTED returns
//   - **an uncashed giro is not a payment.** Handing over a cheque settles nothing; the
//     payable drops when it clears
type PembayaranUtangUseCase struct {
	DB                   *sql.DB
	Log                  *logrus.Logger
	Validate             *validator.Validate
	PembayaranRepository *repository.PembayaranUtangRepository
	PembelianRepository  *repository.PembelianRepository
	CounterRepository    *repository.DocumentCounterRepository
	// UnitKerjaRepository resolves the kode a document number carries — isu #21
	// fase 1. This module has no room of its own, so the number is keyed to the
	// caller's active unit_kerja rather than one read off a document field.
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewPembayaranUtangUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	pembayaranRepository *repository.PembayaranUtangRepository,
	pembelianRepository *repository.PembelianRepository,
	counterRepository *repository.DocumentCounterRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *PembayaranUtangUseCase {
	return &PembayaranUtangUseCase{
		DB:                   db,
		Log:                  log,
		Validate:             validate,
		PembayaranRepository: pembayaranRepository,
		PembelianRepository:  pembelianRepository,
		CounterRepository:    counterRepository,
		UnitKerjaRepository:  unitKerjaRepository,
	}
}

// Create opens a DRAFT payment with its allocations.
//
// The allocations may be empty, and that is a real case rather than a degenerate one:
// money is sometimes paid before it is decided which invoices it covers, and the remainder
// then sits as a credit. That is the same rule as jumlah_dialokasikan being allowed below
// jumlah, applied at the moment of creation.
func (c *PembayaranUtangUseCase) Create(ctx context.Context, request *model.CreatePembayaranUtangRequest) (*model.PembayaranUtangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal, err := time.Parse(dateOnly, request.Tanggal)
	if err != nil {
		return nil, model.Invalid("tanggal harus YYYY-MM-DD")
	}

	jatuhTempo, err := parseTanggalOpsional(request.TanggalJatuhTempoGiro, "tanggal_jatuh_tempo_giro")
	if err != nil {
		return nil, err
	}

	jumlah, err := parseNumeric(request.Jumlah)
	if err != nil {
		return nil, err
	}
	if jumlah.Sign() <= 0 {
		return nil, model.Invalid("jumlah harus lebih dari nol")
	}

	// The giro columns are only meaningful for a giro, and pembayaran_utang_giro_check
	// enforces that at the database. Catching it here means the message names the field
	// rather than a constraint.
	var statusGiro *string

	if request.Metode == entity.MetodePembayaranGiro {
		belumCair := entity.StatusGiroBelumCair
		statusGiro = &belumCair
	} else if jatuhTempo != nil {
		return nil, model.Invalid("tanggal_jatuh_tempo_giro hanya untuk metode GIRO")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	nomor, err := nomorDokumenUntukUnitAktif(
		ctx, tx, c.CounterRepository, c.UnitKerjaRepository,
		repository.PrefixPembayaranUtang, tanggal, request.AktifIDUnitKerja,
	)
	if err != nil {
		return nil, err
	}

	pembayaran := &entity.PembayaranUtang{
		Nomor:                 nomor,
		Tanggal:               tanggal,
		IDSupplier:            request.IDSupplier,
		Metode:                request.Metode,
		NoReferensi:           request.NoReferensi,
		NamaBank:              request.NamaBank,
		TanggalJatuhTempoGiro: jatuhTempo,
		StatusGiro:            statusGiro,
		Jumlah:                formatNumeric(roundNumeric(jumlah, skalaUang), skalaUang),
		Status:                entity.StatusPembayaranUtangDraft,
		Keterangan:            request.Keterangan,
		CreatedBy:             request.ActorID,
	}

	if err := c.PembayaranRepository.Create(ctx, tx, pembayaran); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "nomor dokumen sudah dipakai"),
			"id_supplier tidak ada",
		)
	}

	if err := c.tulisAlokasi(ctx, tx, pembayaran, request.Alokasi); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather than
	// what was sent.
	return c.detail(ctx, c.DB, pembayaran.ID)
}

func (c *PembayaranUtangUseCase) Get(ctx context.Context, request *model.GetPembayaranUtangRequest) (*model.PembayaranUtangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *PembayaranUtangUseCase) Search(ctx context.Context, request *model.ListPembayaranUtangRequest) ([]model.PembayaranUtangResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PembayaranRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.Metode, request.StatusGiro,
		request.IDSupplier, request.TanggalDari, request.TanggalSampai,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PembayaranUtangToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT.
//
// Lowering `jumlah` below what is already allocated is refused here rather than left to
// pembayaran_utang_alokasi_check, so the message says which figure is in the way.
func (c *PembayaranUtangUseCase) Update(ctx context.Context, request *model.UpdatePembayaranUtangRequest) (*model.PembayaranUtangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	for nama, wajib := range map[string]bool{
		"tanggal": request.Tanggal.Clears(),
		"jumlah":  request.Jumlah.Clears(),
	} {
		if wajib {
			return nil, model.Invalid(nama + " cannot be null")
		}
	}

	var patch repository.PembayaranUtangPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	if request.TanggalJatuhTempoGiro.Present {
		patch.SetTanggalJatuhTempoGiro = true

		if request.TanggalJatuhTempoGiro.Value != nil {
			tanggal, err := time.Parse(dateOnly, *request.TanggalJatuhTempoGiro.Value)
			if err != nil {
				return nil, model.Invalid("tanggal_jatuh_tempo_giro harus YYYY-MM-DD")
			}

			patch.TanggalJatuhTempoGiro = &tanggal
		}
	}

	patch.SetNoReferensi = request.NoReferensi.Present
	patch.NoReferensi = request.NoReferensi.Value
	patch.SetNamaBank = request.NamaBank.Present
	patch.NamaBank = request.NamaBank.Value
	patch.SetKeterangan = request.Keterangan.Present
	patch.Keterangan = request.Keterangan.Value

	var jumlahBaru *big.Rat

	if request.Jumlah.Set() {
		nilai, err := parseNumeric(*request.Jumlah.Value)
		if err != nil {
			return nil, err
		}
		if nilai.Sign() <= 0 {
			return nil, model.Invalid("jumlah harus lebih dari nol")
		}

		jumlahBaru = roundNumeric(nilai, skalaUang)
		jumlahText := formatNumeric(jumlahBaru, skalaUang)
		patch.Jumlah = &jumlahText
	}

	if patch.Tanggal == nil && patch.Jumlah == nil && !patch.SetNoReferensi &&
		!patch.SetNamaBank && !patch.SetTanggalJatuhTempoGiro && !patch.SetKeterangan {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	pembayaran, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembayaranUtangDraft)
	if err != nil {
		return nil, err
	}

	if patch.SetTanggalJatuhTempoGiro && patch.TanggalJatuhTempoGiro != nil &&
		pembayaran.Metode != entity.MetodePembayaranGiro {
		return nil, model.Invalid("tanggal_jatuh_tempo_giro hanya untuk metode GIRO")
	}

	if jumlahBaru != nil {
		dialokasikan := mustParseNumeric(pembayaran.JumlahDialokasikan)
		if jumlahBaru.Cmp(dialokasikan) < 0 {
			return nil, model.Invalid(fmt.Sprintf(
				"jumlah %s lebih kecil dari yang sudah dialokasikan, %s; ubah alokasinya lebih dulu",
				formatNumeric(jumlahBaru, skalaUang), pembayaran.JumlahDialokasikan,
			))
		}
	}

	if err := c.PembayaranRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnCheck(
			notFoundOnNoRows(err, "pembayaran utang not found"),
			"nilai pembayaran tidak konsisten dengan metodenya",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceAlokasi swaps the whole allocation set of a DRAFT.
//
// An empty set is accepted, unlike a document's detail lines: a payment with nothing
// allocated is a credit sitting with the supplier, which is a legitimate state.
func (c *PembayaranUtangUseCase) ReplaceAlokasi(ctx context.Context, request *model.ReplacePembayaranUtangAlokasiRequest) (*model.PembayaranUtangResponse, error) {
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

	pembayaran, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembayaranUtangDraft)
	if err != nil {
		return nil, err
	}

	if err := c.PembayaranRepository.DeleteAlokasi(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := c.tulisAlokasi(ctx, tx, pembayaran, request.Alokasi); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Posting settles the payment: it re-checks every allocation against what the invoices
// still owe, closes the document, and rewrites their payment caches.
//
// The remaining-balance check that counts runs here, under each invoice's row lock. The one
// at draft time only produces a friendlier error sooner — between a draft being written and
// posted, another payment or a return can consume the balance it was aimed at.
//
// A giro posts like anything else and settles nothing yet. The document becomes real, the
// allocations become real, and every cache is recomputed — which for a BELUM_CAIR giro
// leaves the payables exactly where they were. That is the point: the paper has changed
// hands, the money has not.
func (c *PembayaranUtangUseCase) Posting(ctx context.Context, request *model.PostingPembayaranUtangRequest) (*model.PembayaranUtangResponse, error) {
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

	pembayaran, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembayaranUtangDraft)
	if err != nil {
		return nil, err
	}

	alokasi, err := c.PembayaranRepository.FindAlokasi(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	// Every allocation is verified again against a locked invoice. Ascending id order is
	// not cosmetic: FindAlokasi orders by id and the recompute below walks the same
	// sequence, so two payments over overlapping invoice sets take the locks in one
	// consistent order and queue rather than deadlock.
	for i := range alokasi {
		if err := c.periksaSisaUtang(
			ctx, tx, pembayaran.IDSupplier, alokasi[i].IDPembelian,
			mustParseNumeric(alokasi[i].Jumlah), request.ID, i+1,
		); err != nil {
			return nil, err
		}
	}

	if err := c.PembayaranRepository.Posting(ctx, tx, request.ID); err != nil {
		return nil, conflictOnTransisi(err, "status pembayaran utang sudah berubah")
	}

	// After the transition, because the recompute counts POSTED payments.
	if err := c.hitungUlangUtang(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted payment. Every invoice it settled goes back to owing what it owed.
//
// Nothing is deleted and no reversing document is written, which is the one place this
// module is genuinely simpler than the stock side: status_pembayaran is a pure function of
// the POSTED rows, so flipping this document out of POSTED and recomputing is exact. There
// is no valuation residue to strand, unlike a kartu_stok reversal.
//
// The allocation rows survive, so the void is auditable: what this payment claimed to
// settle is still readable next to alasan_batal.
func (c *PembayaranUtangUseCase) Batal(ctx context.Context, request *model.BatalPembayaranUtangRequest) (*model.PembayaranUtangResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusPembayaranUtangPosted); err != nil {
		return nil, err
	}

	if err := c.PembayaranRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status pembayaran utang sudah berubah")
	}

	if err := c.hitungUlangUtang(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// CairkanGiro records that a giro cleared the bank, which is the moment the payables it was
// allocated to actually drop.
//
// Until now the allocations existed and counted for nothing. This is why the check against
// each invoice's remaining balance has to run again here: while the giro sat uncashed,
// nothing stopped another payment from settling the same invoice, and both cannot be right.
func (c *PembayaranUtangUseCase) CairkanGiro(ctx context.Context, request *model.CairkanGiroRequest) (*model.PembayaranUtangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggalCair, err := time.Parse(dateOnly, request.TanggalCair)
	if err != nil {
		return nil, model.Invalid("tanggal_cair harus YYYY-MM-DD")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	pembayaran, err := c.kunciGiroBelumCair(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if tanggalCair.Before(pembayaran.Tanggal.Truncate(24 * time.Hour)) {
		return nil, model.Invalid("tanggal_cair tidak boleh sebelum tanggal pembayaran")
	}

	alokasi, err := c.PembayaranRepository.FindAlokasi(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	// The allocations counted for nothing while the giro was outstanding, so the invoices
	// may have been settled by something else in the meantime. This is a real sequence,
	// not a race: a giro can sit for weeks.
	for i := range alokasi {
		if err := c.periksaSisaUtang(
			ctx, tx, pembayaran.IDSupplier, alokasi[i].IDPembelian,
			mustParseNumeric(alokasi[i].Jumlah), request.ID, i+1,
		); err != nil {
			return nil, err
		}
	}

	if err := c.PembayaranRepository.CairkanGiro(ctx, tx, request.ID, tanggalCair); err != nil {
		return nil, conflictOnTransisi(err, "status giro sudah berubah")
	}

	if err := c.hitungUlangUtang(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// TolakGiro records that a giro bounced.
//
// Nothing has to be given back and no cache changes, because an uncashed giro never reduced
// a payable in the first place. The recompute still runs — it is a no-op here by
// construction, and running it anyway is what keeps "every path that could change the
// answer recomputes it" true without exceptions to remember.
func (c *PembayaranUtangUseCase) TolakGiro(ctx context.Context, request *model.TolakGiroRequest) (*model.PembayaranUtangResponse, error) {
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

	if _, err := c.kunciGiroBelumCair(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := c.PembayaranRepository.TolakGiro(ctx, tx, request.ID, request.Alasan); err != nil {
		return nil, conflictOnTransisi(err, "status giro sudah berubah")
	}

	if err := c.hitungUlangUtang(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// detail loads a header and its allocations. Two queries, independent of how many
// allocations come back.
func (c *PembayaranUtangUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.PembayaranUtangResponse, error) {
	pembayaran, err := c.PembayaranRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembayaran utang not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the key.
	// For this document that distinction carries meaning: no allocations is a real state,
	// and it should not look like the question was never asked.
	pembayaran.Alokasi, err = c.PembayaranRepository.FindAlokasi(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.PembayaranUtangToResponse(pembayaran), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *PembayaranUtangUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.PembayaranUtang, error) {
	pembayaran, err := c.PembayaranRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pembayaran utang not found")
	}

	if pembayaran.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, pembayaran utang sekarang %s", diharapkan, pembayaran.Status,
		))
	}

	return pembayaran, nil
}

// kunciGiroBelumCair locks a POSTED giro that has not yet been settled either way.
//
// Three conditions, three different mistakes: a non-giro has nothing to clear, a DRAFT giro
// was never handed over, and one already CAIR or TOLAK has been decided. Naming which one
// the caller hit is the whole reason this is not left to the guarded UPDATE.
func (c *PembayaranUtangUseCase) kunciGiroBelumCair(ctx context.Context, tx repository.DBTX, id int64) (*entity.PembayaranUtang, error) {
	pembayaran, err := c.kunciDenganStatus(ctx, tx, id, entity.StatusPembayaranUtangPosted)
	if err != nil {
		return nil, err
	}

	if pembayaran.Metode != entity.MetodePembayaranGiro {
		return nil, model.Invalid(fmt.Sprintf(
			"aksi ini hanya untuk metode GIRO, pembayaran ini %s", pembayaran.Metode,
		))
	}

	if pembayaran.StatusGiro == nil || *pembayaran.StatusGiro != entity.StatusGiroBelumCair {
		status := "kosong"
		if pembayaran.StatusGiro != nil {
			status = *pembayaran.StatusGiro
		}

		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh giro BELUM_CAIR, giro ini %s", status,
		))
	}

	return pembayaran, nil
}

// tulisAlokasi writes the allocation rows and refreshes the header's allocated total.
//
// Two ceilings are checked, and they are different questions. The payment's own amount
// bounds the sum of its rows; each invoice's remaining balance bounds the row aimed at it.
// A request can satisfy either one and fail the other.
func (c *PembayaranUtangUseCase) tulisAlokasi(ctx context.Context, tx repository.DBTX, pembayaran *entity.PembayaranUtang, requests []model.PembayaranUtangAlokasiRequest) error {
	jumlahPembayaran := mustParseNumeric(pembayaran.Jumlah)
	total := new(big.Rat)

	// A repeat inside one request would raise 23505 from
	// pembayaran_utang_alokasi_baris_uidx, naming an index. Catching it first says what the
	// caller actually got wrong — and without the index, two rows for the same invoice
	// would each pass the remaining-balance check on their own.
	terlihat := make(map[int64]int, len(requests))

	rows := make([]entity.PembayaranUtangAlokasi, 0, len(requests))

	for i := range requests {
		baris := &requests[i]

		if sebelumnya, ulang := terlihat[baris.IDPembelian]; ulang {
			return model.Invalid(fmt.Sprintf(
				"alokasi %d: pembelian yang sama sudah dipakai di alokasi %d", i+1, sebelumnya,
			))
		}

		terlihat[baris.IDPembelian] = i + 1

		jumlah, err := parseNumeric(baris.Jumlah)
		if err != nil {
			return err
		}
		if jumlah.Sign() <= 0 {
			return model.Invalid(fmt.Sprintf("alokasi %d: jumlah harus lebih dari nol", i+1))
		}

		jumlah = roundNumeric(jumlah, skalaUang)

		if err := c.periksaSisaUtang(
			ctx, tx, pembayaran.IDSupplier, baris.IDPembelian, jumlah, pembayaran.ID, i+1,
		); err != nil {
			return err
		}

		total.Add(total, jumlah)

		rows = append(rows, entity.PembayaranUtangAlokasi{
			IDPembayaranUtang: pembayaran.ID,
			IDPembelian:       baris.IDPembelian,
			Jumlah:            formatNumeric(jumlah, skalaUang),
		})
	}

	// Over-allocation is refused; under-allocation is not. The remainder is a credit with
	// the supplier, and forcing it to balance would make the common case impossible.
	if total.Cmp(jumlahPembayaran) > 0 {
		return model.Invalid(fmt.Sprintf(
			"total alokasi %s melebihi jumlah pembayaran %s",
			formatNumeric(total, skalaUang), pembayaran.Jumlah,
		))
	}

	for i := range rows {
		if err := c.PembayaranRepository.InsertAlokasi(ctx, tx, &rows[i]); err != nil {
			return invalidOnForeignKey(
				conflictOnUnique(err, "pembelian yang sama dipakai dua kali"),
				"id_pembelian tidak ada",
			)
		}
	}

	if err := c.PembayaranRepository.RecalculateDialokasikan(ctx, tx, pembayaran.ID); err != nil {
		return invalidOnCheck(err, "total alokasi melebihi jumlah pembayaran")
	}

	// Keep the in-memory header in step, so a caller that goes on to compare against it
	// does not reason from a stale figure.
	pembayaran.JumlahDialokasikan = formatNumeric(roundNumeric(total, skalaUang), skalaUang)

	return nil
}

// periksaSisaUtang refuses an allocation larger than what an invoice still owes.
//
// It takes the invoice's row lock first, and that ordering is the whole point: the balance
// is derived from rows in two other tables, so reading it without the lock lets two
// payments both find room that only one of them has.
//
// kecualiID excludes the payment being worked on. Without it, re-posting or clearing a
// document whose own allocations already count would measure its share against a balance it
// has itself already consumed, and a correct payment would be refused.
func (c *PembayaranUtangUseCase) periksaSisaUtang(ctx context.Context, tx repository.DBTX, idSupplier, idPembelian int64, jumlah *big.Rat, kecualiID int64, nomorBaris int) error {
	if _, err := c.PembelianRepository.LockByID(ctx, tx, idPembelian); err != nil {
		return notFoundOnNoRows(err, fmt.Sprintf(
			"alokasi %d: pembelian tidak ditemukan", nomorBaris,
		))
	}

	sisa, err := c.PembelianRepository.FindSisaUtang(ctx, tx, idPembelian)
	if err != nil {
		return notFoundOnNoRows(err, fmt.Sprintf(
			"alokasi %d: pembelian tidak ditemukan", nomorBaris,
		))
	}

	// A DRAFT invoice is a typed page rather than a debt, and a BATAL one is a debt that
	// was withdrawn. Neither can be paid.
	if sisa.Status != entity.StatusPembelianPosted {
		return model.Invalid(fmt.Sprintf(
			"alokasi %d: pembelian harus berstatus POSTED, pembelian ini %s", nomorBaris, sisa.Status,
		))
	}

	// Paying another supplier's invoice from this supplier's payment would make both
	// ledgers wrong at once, and the header's id_supplier is what the payable is reported
	// under.
	if sisa.IDSupplier != idSupplier {
		return model.Invalid(fmt.Sprintf(
			"alokasi %d: pembelian itu milik supplier lain", nomorBaris,
		))
	}

	terpakai, err := c.PembayaranRepository.SumAlokasiEfektifKecuali(ctx, tx, idPembelian, kecualiID)
	if err != nil {
		return err
	}

	tersisa := mustParseNumeric(sisa.Total)
	tersisa.Sub(tersisa, mustParseNumeric(terpakai))
	tersisa.Sub(tersisa, mustParseNumeric(sisa.NilaiKreditRetur))

	if tersisa.Sign() <= 0 {
		return model.Invalid(fmt.Sprintf(
			"alokasi %d: pembelian itu sudah tidak punya sisa utang", nomorBaris,
		))
	}

	if jumlah.Cmp(tersisa) > 0 {
		return model.Invalid(fmt.Sprintf(
			"alokasi %d: jumlah %s melebihi sisa utang %s",
			nomorBaris, formatNumeric(jumlah, skalaUang), formatNumeric(tersisa, skalaUang),
		))
	}

	return nil
}

// hitungUlangUtang rewrites status_pembayaran on every invoice this payment points at.
//
// Called after posting, after voiding, and after a giro is settled either way — every path
// that can change the answer, with no exceptions to remember. Ascending invoice order fixes
// one lock sequence across all four.
func (c *PembayaranUtangUseCase) hitungUlangUtang(ctx context.Context, tx repository.DBTX, id int64) error {
	ids, err := c.PembayaranRepository.FindIDPembelianAlokasi(ctx, tx, id)
	if err != nil {
		return err
	}

	for _, idPembelian := range ids {
		if err := c.PembelianRepository.RecalculateStatusPembayaran(ctx, tx, idPembelian); err != nil {
			return err
		}
	}

	return nil
}
