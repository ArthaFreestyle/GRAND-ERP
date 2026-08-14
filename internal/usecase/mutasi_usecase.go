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

// MutasiUseCase owns the transfer between rooms — isu #7.
//
// It is the fourth document to write kartu_stok and the first to write in both
// directions at once. One mutasi_detail line becomes two ledger rows: MUTASI_KELUAR in
// the source room and MUTASI_MASUK in the destination, in one transaction. That is
// migration 000007's own description of the table, and it is what makes the transfer
// conserve stock: split into two documents, goods could leave a warehouse without ever
// arriving at the shop, and nothing would hold the halves together.
//
// Three things it deliberately does not have, all of which a reader arriving from
// pembelian will expect:
//
//   - No DIAJUKAN. See entity.StatusMutasiDraft.
//   - No money arithmetic at all. There is no subtotal, discount, PPN, freight, or koli
//     here, so nothing needs proportional allocation and pembelian_alokasi.go has
//     nothing to offer. The single decimal figure in the module is a cost that comes
//     back out of the database.
//   - No parent document and no quota drawn from one. What limits a line is the source
//     room's balance, and the kartu_stok trigger is what enforces it.
//
// It takes no ProductRepository quota check either; FindFaktorBatch is the only thing
// product is asked for.
type MutasiUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	MutasiRepository    *repository.MutasiRepository
	ProductRepository   *repository.ProductRepository
	KartuStokRepository *repository.KartuStokRepository
	CounterRepository   *repository.DocumentCounterRepository
	// Read for the error message only; the kartu_stok trigger is the guard. See
	// PembelianUseCase.
	PeriodeRepository *repository.PeriodeRepository
}

func NewMutasiUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	mutasiRepository *repository.MutasiRepository,
	productRepository *repository.ProductRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
	periodeRepository *repository.PeriodeRepository,
) *MutasiUseCase {
	return &MutasiUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		MutasiRepository:    mutasiRepository,
		ProductRepository:   productRepository,
		KartuStokRepository: kartuStokRepository,
		CounterRepository:   counterRepository,
		PeriodeRepository:   periodeRepository,
	}
}

// Create opens a DRAFT transfer, optionally with its lines.
//
// Lines are optional here and mandatory on ReplaceDetail, which is not an
// inconsistency: a transfer is commonly opened while the trolley is still being loaded,
// and posting refuses a document with no lines anyway. Replacing the line set with
// nothing, on the other hand, is asking to empty a document through a path that means
// "these are the lines".
func (c *MutasiUseCase) Create(ctx context.Context, request *model.CreateMutasiRequest) (*model.MutasiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal, err := time.Parse(dateOnly, request.Tanggal)
	if err != nil {
		return nil, model.Invalid("tanggal harus YYYY-MM-DD")
	}

	// mutasi_ruang_check would reject this too, but a CHECK violation names a constraint
	// and cannot say which field is wrong. Same relationship ExistsByKode has to a unique
	// index: the pre-check exists only for the message.
	if request.IDRuangAsal == request.IDRuangTujuan {
		return nil, model.Invalid("id_ruang_asal dan id_ruang_tujuan tidak boleh sama")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	nomor, err := nomorDokumen(ctx, tx, c.CounterRepository, repository.PrefixMutasi, tanggal)
	if err != nil {
		return nil, err
	}

	mutasi := &entity.Mutasi{
		Nomor:         nomor,
		Tanggal:       tanggal,
		IDRuangAsal:   request.IDRuangAsal,
		IDRuangTujuan: request.IDRuangTujuan,
		Status:        entity.StatusMutasiDraft,
		CreatedBy:     request.ActorID,
	}

	if request.Keterangan != "" {
		keterangan := request.Keterangan
		mutasi.Keterangan = &keterangan
	}

	if err := c.MutasiRepository.Create(ctx, tx, mutasi); err != nil {
		return nil, invalidOnForeignKey(
			invalidOnCheck(
				conflictOnUnique(err, "nomor dokumen sudah dipakai"),
				"id_ruang_asal dan id_ruang_tujuan tidak boleh sama",
			),
			"id_ruang_asal atau id_ruang_tujuan tidak ada di tabel ruang",
		)
	}

	detail, err := c.siapkanDetail(ctx, tx, mutasi.ID, request.Detail)
	if err != nil {
		return nil, err
	}

	if err := c.tulisDetail(ctx, tx, detail); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the stored rows and their joined names rather than
	// what was sent.
	return c.detail(ctx, c.DB, mutasi.ID)
}

func (c *MutasiUseCase) Get(ctx context.Context, request *model.GetMutasiRequest) (*model.MutasiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

func (c *MutasiUseCase) Search(ctx context.Context, request *model.ListMutasiRequest) ([]model.MutasiResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.MutasiRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDRuangAsal, request.IDRuangTujuan,
		request.TanggalDari, request.TanggalSampai, request.TerlamaDulu,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.MutasiToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches the header of a DRAFT.
//
// Both rooms may move, unlike pembayaran_utang where the supplier is fixed for the life
// of the document. Nothing in mutasi_detail names a room, so changing one leaves nothing
// behind pointing at the wrong place.
func (c *MutasiUseCase) Update(ctx context.Context, request *model.UpdateMutasiRequest) (*model.MutasiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// All three are NOT NULL columns: changeable, never clearable.
	if request.Tanggal.Clears() {
		return nil, model.Invalid("tanggal cannot be null")
	}
	if request.IDRuangAsal.Clears() {
		return nil, model.Invalid("id_ruang_asal cannot be null")
	}
	if request.IDRuangTujuan.Clears() {
		return nil, model.Invalid("id_ruang_tujuan cannot be null")
	}

	var patch repository.MutasiPatch

	if request.Tanggal.Set() {
		tanggal, err := time.Parse(dateOnly, *request.Tanggal.Value)
		if err != nil {
			return nil, model.Invalid("tanggal harus YYYY-MM-DD")
		}

		patch.Tanggal = &tanggal
	}

	patch.IDRuangAsal = request.IDRuangAsal.Value
	patch.IDRuangTujuan = request.IDRuangTujuan.Value
	patch.SetKeterangan = request.Keterangan.Set()
	patch.Keterangan = request.Keterangan.Value

	if patch.Tanggal == nil && patch.IDRuangAsal == nil && patch.IDRuangTujuan == nil && !patch.SetKeterangan {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	mutasi, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusMutasiDraft)
	if err != nil {
		return nil, err
	}

	// Checked against the stored row, not against the patch: moving only one of the two
	// can collide with whichever is already there, and a patch that changes neither
	// cannot collide at all. mutasi_ruang_check is still the guard; this is the message.
	asal, tujuan := mutasi.IDRuangAsal, mutasi.IDRuangTujuan
	if patch.IDRuangAsal != nil {
		asal = *patch.IDRuangAsal
	}
	if patch.IDRuangTujuan != nil {
		tujuan = *patch.IDRuangTujuan
	}

	if asal == tujuan {
		return nil, model.Invalid("id_ruang_asal dan id_ruang_tujuan tidak boleh sama")
	}

	if err := c.MutasiRepository.UpdateHeader(ctx, tx, request.ID, patch); err != nil {
		return nil, invalidOnForeignKey(
			invalidOnCheck(
				notFoundOnNoRows(err, "mutasi not found"),
				"id_ruang_asal dan id_ruang_tujuan tidak boleh sama",
			),
			"id_ruang_asal atau id_ruang_tujuan tidak ada di tabel ruang",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// ReplaceDetail swaps the whole line set of a DRAFT.
func (c *MutasiUseCase) ReplaceDetail(ctx context.Context, request *model.ReplaceMutasiDetailRequest) (*model.MutasiResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusMutasiDraft); err != nil {
		return nil, err
	}

	if err := c.MutasiRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
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

// Posting moves the goods: two kartu_stok rows per line, out of the source room and
// into the destination, in one transaction.
//
// The order of the two inserts is forced, and the constraint is what makes the module
// correct. Migration 000007 states the rule — cost must follow the source room, or
// moving goods would change the total value of inventory — but the application cannot
// compute that cost. The source room's moving average is known only to
// kartu_stok_hitung_saldo, which reads it inside a pg_advisory_xact_lock, so anything
// read beforehand may already be stale; and the trigger overwrites nilai_keluar and
// harga_pokok_satuan on every outgoing row, so a figure supplied here is discarded.
//
// What saves it is that Insert returns the whole row back, including what the trigger
// computed. So the outgoing row is written first and the incoming row is valued at
// exactly what the ledger recorded leaving:
//
//	nilai_masuk (tujuan) = nilai_keluar (asal)
//
// That single assignment is the conservation law of this module. Σ nilai_akhir across
// both rooms does not move, which is the most important test in isu #7.
// ReturPembelianUseCase.Batal fills NilaiMasuk from a NilaiKeluar for the same reason:
// what the ledger recorded leaving is the one figure that makes a pair sum to zero.
//
// mutasi_detail.harga_pokok_satuan_dasar follows from the same ordering — it is written
// here, not at draft time, from the cost the outgoing row reported.
func (c *MutasiUseCase) Posting(ctx context.Context, request *model.PostingMutasiRequest) (*model.MutasiResponse, error) {
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

	mutasi, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusMutasiDraft)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the status
	// column being right. Posting twice moves the goods twice, and kartu_stok is
	// append-only.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTableMutasi, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("mutasi ini sudah punya baris kartu stok")
	}

	detail, err := c.MutasiRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	if len(detail) == 0 {
		return nil, model.Invalid("mutasi tanpa baris tidak bisa diposting")
	}

	if err := c.kunciJalurStok(ctx, tx, mutasi, detail, mutasi.Tanggal); err != nil {
		return nil, err
	}

	// Checked so the refusal can name the period; the trigger stays the guard.
	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, mutasi.Tanggal); err != nil {
		return nil, err
	}

	if err := c.periksaSaldo(ctx, tx, mutasi.IDRuangAsal, detail); err != nil {
		return nil, err
	}

	for i := range detail {
		qtyInput := detail[i].QtyInput
		satuanInput := detail[i].IDSatuanInput

		keluar := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          mutasi.IDRuangAsal,
			TanggalTransaksi: mutasi.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiMutasiKeluar,
			StokKeluar:       detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// Zero, not a cost of our own. This is an outgoing row: the trigger computes
			// what leaves from the running average, and nilai_masuk is NOT NULL so it has
			// to be sent as an explicit zero rather than left empty.
			NilaiMasuk:     "0",
			RefTable:       entity.RefTableMutasi,
			RefIDTransaksi: mutasi.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, keluar); err != nil {
			return nil, invalidOnCheck(
				err,
				"posting ditolak kartu stok: stok di ruang asal tidak mencukupi atau periode sudah TUTUP",
			)
		}

		masuk := &entity.KartuStok{
			IDBarang:         detail[i].IDProduct,
			IDRuang:          mutasi.IDRuangTujuan,
			TanggalTransaksi: mutasi.Tanggal,
			JenisTransaksi:   entity.JenisTransaksiMutasiMasuk,
			StokMasuk:        detail[i].QtyDasar,
			QtyInput:         &qtyInput,
			IDSatuanInput:    &satuanInput,
			// The conservation law. Exactly what the ledger recorded leaving the source
			// room, never a figure recomputed here and never the cost typed on a draft:
			// either would let the total value of inventory change because goods changed
			// shelf.
			NilaiMasuk:     keluar.NilaiKeluar,
			RefTable:       entity.RefTableMutasi,
			RefIDTransaksi: mutasi.ID,
			CreatedBy:      request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, masuk); err != nil {
			return nil, invalidOnCheck(
				err,
				"posting ditolak kartu stok: periode sudah TUTUP",
			)
		}

		// The cost the source room actually carried, now that the trigger has said what
		// it was. Nullable in the schema precisely so a DRAFT can go without it.
		if err := c.MutasiRepository.UpdateDetailPosting(
			ctx, tx, detail[i].ID, keluar.HargaPokokSatuan,
		); err != nil {
			return nil, err
		}
	}

	if err := c.MutasiRepository.Posting(ctx, tx, request.ID); err != nil {
		return nil, conflictOnTransisi(err, "status mutasi sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// Batal voids a posted transfer by appending a reversing row for every movement it
// made: the goods go back to the room they came from.
//
// Two things about it are worth knowing before relying on it, and neither can be
// engineered away.
//
// It is not symmetric in value. The reversing row leaving the destination room is
// valued at that room's moving average right now, which may have shifted because other
// goods arrived there in the meantime. So a transfer and its cancellation do not always
// sum to zero in value, only in quantity. This is the same limitation already recorded
// for pembelian: kartu_stok_hitung_saldo owns the cost of every outgoing row, and
// copying the original is simply not available. Undoing it would need a different
// valuation method, not a different line of Go.
//
// And it can be refused outright. If the goods have since left the destination room, the
// reversing row drives that room's balance negative and the trigger rejects it — 400,
// through invalidOnCheck. That is correct: goods already sold out of the shop cannot be
// put back in the warehouse by voiding the transfer that brought them there. The
// remedy is another mutasi, not this one.
//
// Dated today rather than on the document, like every reversal in this codebase, so a
// transfer whose period has since closed can still be voided — into the current period.
// The cost is stated at PembelianUseCase.Batal: the document reads BATAL while the
// closed month's ledger still carries its movements, so anything reporting per period
// must read kartu_stok and never a document status.
func (c *MutasiUseCase) Batal(ctx context.Context, request *model.BatalMutasiRequest) (*model.MutasiResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusMutasiPosted); err != nil {
		return nil, err
	}

	asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTableMutasi, request.ID)
	if err != nil {
		return nil, err
	}

	// The reversal is dated now, so the period that has to be open is the current one,
	// not the document's — and the balance locks are taken for the pairs those reversing
	// rows will touch, which is the same set the posting touched.
	tanggalPembalik := time.Now()

	if err := c.kunciJalurPembalik(ctx, tx, asal, tanggalPembalik); err != nil {
		return nil, err
	}

	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, tanggalPembalik); err != nil {
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
			TanggalTransaksi: tanggalPembalik,
			JenisTransaksi:   entity.JenisTransaksiPembatalanTransaksi,
			// Mirrored: what went out comes back in and vice versa, valued at what the
			// ledger recorded moving. For the row reversing MUTASI_KELUAR that is exact.
			// For the one reversing MUTASI_MASUK the trigger will overwrite it with the
			// destination room's current average, which is the asymmetry described above.
			StokMasuk:       asal[i].StokKeluar,
			StokKeluar:      asal[i].StokMasuk,
			NilaiMasuk:      asal[i].NilaiKeluar,
			RefTable:        entity.RefTableMutasi,
			RefIDTransaksi:  request.ID,
			IDKartuStokAsal: &idAsal,
			Keterangan:      &keterangan,
			CreatedBy:       request.ActorID,
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
			return nil, invalidOnCheck(
				err,
				"pembatalan ditolak kartu stok: barang sudah keluar lagi dari ruang tujuan atau periode sudah TUTUP",
			)
		}
	}

	if err := c.MutasiRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status mutasi sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID)
}

// kunciJalurStok takes every lock this document's inserts will need, up front, in the
// order every writer in the system uses.
//
// This is the one piece of concurrency control mutasi needs that no earlier module did,
// and it is not a micro-optimisation. The kartu_stok trigger takes an advisory lock per
// (id_barang, id_ruang), and a mutasi takes two of them for the same product — in an
// order decided by which way the goods are moving:
//
//	dokumen A (gudang -> toko) : (X, gudang) then (X, toko)
//	dokumen B (toko -> gudang) : (X, toko)   then (X, gudang)
//
// Textbook ABBA, and it could not happen before because every other document touched one
// room. Two opposite transfers of the same product, posted at the same moment, would
// deadlock. Taking all of them before the first insert, sorted, makes that impossible
// rather than merely unlikely — and the alternative, mapping SQLSTATE 40P01 to a 409 and
// asking the client to retry, hands a real defect to whoever is standing at the counter.
//
// The periode lock comes first, because that is the order the trigger takes them in on
// every insert and a writer that inverted it would open a different cycle: a book
// closing queued for the exclusive periode lock, a posting holding it shared and waiting
// on our balance lock, and us holding that balance lock and waiting behind the closing.
// Uniform order, no cycle.
func (c *MutasiUseCase) kunciJalurStok(ctx context.Context, tx repository.DBTX, mutasi *entity.Mutasi, detail []entity.MutasiDetail, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(detail)*2)
	for i := range detail {
		keys = append(keys,
			repository.SaldoKey{IDBarang: detail[i].IDProduct, IDRuang: mutasi.IDRuangAsal},
			repository.SaldoKey{IDBarang: detail[i].IDProduct, IDRuang: mutasi.IDRuangTujuan},
		)
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// kunciJalurPembalik is kunciJalurStok for a cancellation, where the pairs to lock are
// read off the rows being reversed rather than off the document's lines. Same ordering
// argument, and the same ABBA risk between two cancellations going opposite ways.
func (c *MutasiUseCase) kunciJalurPembalik(ctx context.Context, tx repository.DBTX, asal []entity.KartuStok, tanggal time.Time) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, tanggal); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(asal))
	for i := range asal {
		keys = append(keys, repository.SaldoKey{
			IDBarang: asal[i].IDBarang,
			IDRuang:  asal[i].IDRuang,
		})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// periksaSaldo refuses a transfer the source room cannot cover, before the trigger has
// to.
//
// Not a guard, and it must not be read as one — the trigger is, and it decides inside
// the advisory lock that no reader can get in front of. Same relationship periksaPeriode
// has to a closed period. What it buys is a refusal naming the product and both figures,
// where the trigger's RAISE carries no constraint name and arrives as
// "stock is short or the period is closed".
//
// It runs after kunciJalurStok, which matters: with the balance locks already held, what
// it reads cannot move underneath it for the rest of the transaction, so the message it
// produces is not merely friendlier but actually true.
//
// One query for the whole document rather than one per line — a transfer can carry
// hundreds of lines, and this is inside a transaction already holding locks. Lines are
// summed per product first, because the same product may appear more than once and each
// line fitting on its own says nothing about them fitting together.
func (c *MutasiUseCase) periksaSaldo(ctx context.Context, tx repository.DBTX, idRuangAsal int64, detail []entity.MutasiDetail) error {
	diminta := make(map[int64]int64, len(detail))
	barangIDs := make([]int64, 0, len(detail))
	ruangIDs := make([]int64, 0, len(detail))

	for i := range detail {
		if _, ada := diminta[detail[i].IDProduct]; !ada {
			barangIDs = append(barangIDs, detail[i].IDProduct)
			ruangIDs = append(ruangIDs, idRuangAsal)
		}

		diminta[detail[i].IDProduct] += detail[i].QtyDasar
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, tx, barangIDs, ruangIDs)
	if err != nil {
		return err
	}

	// Walked in line order rather than over the map, so the message names the first line
	// an operator would look at rather than whichever key Go happened to visit first.
	dilaporkan := make(map[int64]bool, len(diminta))

	for i := range detail {
		idProduct := detail[i].IDProduct
		if dilaporkan[idProduct] {
			continue
		}

		dilaporkan[idProduct] = true

		// A pair with no rows at all is a balance of zero, which is the same reading the
		// trigger takes when it COALESCEs a missing previous row.
		tersedia := saldo[repository.SaldoKey{IDBarang: idProduct, IDRuang: idRuangAsal}].StokAkhir

		if diminta[idProduct] > tersedia {
			return model.Invalid(fmt.Sprintf(
				"baris %d: %s butuh %d satuan dasar, saldo ruang asal %d",
				i+1, detail[i].NamaProduct, diminta[idProduct], tersedia,
			))
		}
	}

	return nil
}

// detail loads a header and its lines. Two queries, independent of how many lines come
// back.
func (c *MutasiUseCase) detail(ctx context.Context, db repository.DBTX, id int64) (*model.MutasiResponse, error) {
	mutasi, err := c.MutasiRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "mutasi not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the key
	// and implying the document was never asked about.
	mutasi.Detail, err = c.MutasiRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.MutasiToResponse(mutasi), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong state.
func (c *MutasiUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.Mutasi, error) {
	mutasi, err := c.MutasiRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "mutasi not found")
	}

	if mutasi.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, mutasi sekarang %s", diharapkan, mutasi.Status,
		))
	}

	return mutasi, nil
}

// siapkanDetail turns request lines into rows: it resolves the conversion factor and
// converts the quantity to base units.
//
// Shorter than its counterparts in penerimaan_susulan and retur_pembelian by exactly
// what a parent document would have contributed. There is no source line to resolve, no
// quota to draw down, and no cost to copy — the cost of a transfer is not known until
// the trigger says so at posting.
//
// Repeats of the same product are allowed, unlike the three documents with a per-line
// quota. Here the limit is the source room's balance, checked in total at posting, and
// two lines for one product in different units is a legitimate way to type a transfer.
func (c *MutasiUseCase) siapkanDetail(ctx context.Context, tx repository.DBTX, idMutasi int64, requests []model.MutasiDetailRequest) ([]entity.MutasiDetail, error) {
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

	rows := make([]entity.MutasiDetail, 0, len(requests))

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

		rows = append(rows, entity.MutasiDetail{
			IDMutasi:       idMutasi,
			IDProduct:      baris.IDProduct,
			QtyInput:       formatNumeric(qtyInput, skalaQty),
			IDSatuanInput:  baris.IDSatuanInput,
			FaktorKonversi: konversi,
			QtyDasar:       ratKeInt64(qtyDasar),
		})
	}

	return rows, nil
}

// tulisDetail inserts the lines. There is no header total to refresh afterwards — the
// table has no money column at all.
func (c *MutasiUseCase) tulisDetail(ctx context.Context, tx repository.DBTX, detail []entity.MutasiDetail) error {
	for i := range detail {
		if err := c.MutasiRepository.InsertDetail(ctx, tx, &detail[i]); err != nil {
			return invalidOnForeignKey(err, "id_product atau id_satuan_input tidak ada")
		}
	}

	return nil
}
