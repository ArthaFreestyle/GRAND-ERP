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

// StokOpnameUseCase owns the physical stock count — isu #15. It is the seventh
// document to write kartu_stok, the first that moves goods to or from nowhere at
// all, and the only one that, while open, freezes what every OTHER module may do
// to its room.
//
// Three things set it apart from every earlier writer:
//
//   - The freeze. While Status is DRAFT or DIAJUKAN, the kartu_stok trigger
//     (migration 000023) refuses any posting naming this document's IDRuang from
//     any module — enforced in the database, not by a call this usecase makes.
//     periksaRuangBeku below is this module's own use of that same check, for the
//     message, at Posting and Batal — the identical relationship periksaPeriode
//     has to a closed period.
//   - What is posted is a selisih against a frozen stok_awal, verified unchanged
//     at posting time — never a value the room's balance is forced to. See
//     Posting for the check that makes this an assertion rather than a
//     correction of convenience.
//   - It has no ProductRepository at all. Every other document converts a typed
//     quantity through product_satuan; here the count is always in the base unit,
//     compared directly against kartu_stok's own balance, so there is no
//     conversion to resolve.
type StokOpnameUseCase struct {
	DB                   *sql.DB
	Log                  *logrus.Logger
	Validate             *validator.Validate
	StokOpnameRepository *repository.StokOpnameRepository
	KartuStokRepository  *repository.KartuStokRepository
	CounterRepository    *repository.DocumentCounterRepository
	PeriodeRepository    *repository.PeriodeRepository
	RuangRepository      *repository.RuangRepository
}

func NewStokOpnameUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	stokOpnameRepository *repository.StokOpnameRepository,
	kartuStokRepository *repository.KartuStokRepository,
	counterRepository *repository.DocumentCounterRepository,
	periodeRepository *repository.PeriodeRepository,
	ruangRepository *repository.RuangRepository,
) *StokOpnameUseCase {
	return &StokOpnameUseCase{
		DB:                   db,
		Log:                  log,
		Validate:             validate,
		StokOpnameRepository: stokOpnameRepository,
		KartuStokRepository:  kartuStokRepository,
		CounterRepository:    counterRepository,
		PeriodeRepository:    periodeRepository,
		RuangRepository:      ruangRepository,
	}
}

// Create opens a count session: it reserves a number, takes a cutoff snapshot
// instant, and freezes IDRuang from that moment on — the freeze exists the
// instant the row is visible to other transactions, since the kartu_stok trigger
// reads stok_opname directly rather than a cache of it.
//
// The exclusive ruang: lock is taken before the header is written, not after, so
// a posting already in flight for this room finishes first and the cutoff really
// does cover everything that happened before it — the same reasoning
// PeriodeRepository.Lock applies to closing a month.
func (c *StokOpnameUseCase) Create(ctx context.Context, request *model.CreateStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	if err := periksaRuangUnitAktif(ctx, tx, c.RuangRepository, request.AktifIDUnitKerja, request.IDRuang); err != nil {
		return nil, err
	}

	if err := c.RuangRepository.Lock(ctx, tx, request.IDRuang); err != nil {
		return nil, err
	}

	now := time.Now()

	nomor, err := nomorDokumen(ctx, tx, c.CounterRepository, repository.PrefixStokOpname, now)
	if err != nil {
		return nil, err
	}

	opname := &entity.StokOpname{
		Nomor:     nomor,
		IDRuang:   request.IDRuang,
		TglBuka:   now,
		TsCutoff:  now,
		UraianSO:  request.UraianSO,
		Status:    entity.StatusStokOpnameDraft,
		CreatedBy: request.ActorID,
	}

	if err := c.StokOpnameRepository.Create(ctx, tx, opname); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "ruang ini masih punya stok_opname yang belum selesai"),
			"id_ruang tidak ada",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, opname.ID, nil)
}

func (c *StokOpnameUseCase) Get(ctx context.Context, request *model.GetStokOpnameRequest) (*model.StokOpnameResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, request.AktifIDUnitKerja)
}

func (c *StokOpnameUseCase) Search(ctx context.Context, request *model.ListStokOpnameRequest) ([]model.StokOpnameResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.StokOpnameRepository.Search(
		ctx, c.DB,
		request.Search, request.Status, request.IDRuang,
		request.TanggalDari, request.TanggalSampai, request.AktifIDUnitKerja,
		request.TerlamaDulu, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.StokOpnameToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Update patches uraian_so on a DRAFT — the only header field a PATCH may touch.
func (c *StokOpnameUseCase) Update(ctx context.Context, request *model.UpdateStokOpnameRequest) (*model.StokOpnameResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if !request.UraianSO.Present {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDraft); err != nil {
		return nil, err
	}

	if err := c.StokOpnameRepository.UpdateUraian(ctx, tx, request.ID, true, request.UraianSO.Value); err != nil {
		return nil, notFoundOnNoRows(err, "stok_opname not found")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// TarikSaldo seeds the document's lines from the room's own balance, one row per
// product that has ever moved through it, StokAwal frozen from what
// KartuStokRepository.SaldoRuang reports right now.
//
// "Right now" is correct here, and only here, because of the freeze: this can
// only run on a DRAFT, and the room has been frozen since Create — no other
// module could have written a kartu_stok row for it since. So "the balance right
// now" and "the balance at ts_cutoff" are the same figure by construction, and no
// timestamp-scoped query is needed to prove it.
//
// Refuses to run a second time (StokOpnameRepository.HasDetail): pulling twice
// is the cleanest way to end up with two snapshots inside what is supposed to be
// one document.
func (c *StokOpnameUseCase) TarikSaldo(ctx context.Context, request *model.TarikSaldoStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	opname, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDraft)
	if err != nil {
		return nil, err
	}

	ada, err := c.StokOpnameRepository.HasDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}
	if ada {
		return nil, model.Conflict("stok_opname ini sudah punya baris; tarik-saldo hanya boleh sekali")
	}

	saldo, err := c.KartuStokRepository.SaldoRuang(ctx, tx, opname.IDRuang)
	if err != nil {
		return nil, err
	}

	for i := range saldo {
		detail := &entity.StokOpnameDetail{
			IDStokOpname:      opname.ID,
			IDBarang:          saldo[i].IDBarang,
			IDRuang:           opname.IDRuang,
			StokAwal:          saldo[i].StokAkhir,
			IDKartuStokCutoff: saldo[i].IDKartuStok,
		}

		if err := c.StokOpnameRepository.InsertDetail(ctx, tx, detail); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// ReplaceDetail swaps the whole line set of a DRAFT. This is also how a line
// TarikSaldo's automatic pull missed gets added by hand: a client resends
// TarikSaldo's own result plus whatever else was found on the shelf.
//
// Every line named must already have a kartu_stok row for (product, this room) —
// the same rule TarikSaldo is bound by, and for the identical reason: a product
// the system has never seen in this room has no reference to count against and
// no way to know its cost. See "Barang yang sistem belum pernah lihat" in
// CLAUDE.md.
func (c *StokOpnameUseCase) ReplaceDetail(ctx context.Context, request *model.ReplaceStokOpnameDetailRequest) (*model.StokOpnameResponse, error) {
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

	opname, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDraft)
	if err != nil {
		return nil, err
	}

	seen := make(map[int64]bool, len(request.Detail))
	for i := range request.Detail {
		id := request.Detail[i].IDProduct
		if seen[id] {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: id_product %d muncul dua kali dalam satu dokumen", i+1, id,
			))
		}

		seen[id] = true
	}

	saldo, err := c.KartuStokRepository.SaldoRuang(ctx, tx, opname.IDRuang)
	if err != nil {
		return nil, err
	}

	referensi := make(map[int64]entity.SaldoRuangBaris, len(saldo))
	for i := range saldo {
		referensi[saldo[i].IDBarang] = saldo[i]
	}

	if err := c.StokOpnameRepository.DeleteDetail(ctx, tx, request.ID); err != nil {
		return nil, err
	}

	for i := range request.Detail {
		idProduct := request.Detail[i].IDProduct

		acuan, ada := referensi[idProduct]
		if !ada {
			return nil, model.Invalid(fmt.Sprintf(
				"baris %d: barang %d belum pernah bergerak di ruang ini dan tidak bisa diopname; "+
					"masukkan lewat pembelian atau mutasi setelah opname ini ditutup",
				i+1, idProduct,
			))
		}

		detail := &entity.StokOpnameDetail{
			IDStokOpname:      opname.ID,
			IDBarang:          idProduct,
			IDRuang:           opname.IDRuang,
			StokAwal:          acuan.StokAkhir,
			IDKartuStokCutoff: acuan.IDKartuStok,
		}

		if err := c.StokOpnameRepository.InsertDetail(ctx, tx, detail); err != nil {
			return nil, conflictOnUnique(err, fmt.Sprintf(
				"baris %d: id_product %d sudah ada di dokumen ini", i+1, idProduct,
			))
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// UpdateDetail fills in one line's physical count and/or its note — the
// deliberate exception to "lines are replaced wholesale" every other document in
// this API follows. See model.UpdateStokOpnameDetailRequest.
//
// stok_selisih_lebih/stok_selisih_kurang are recomputed by the UPDATE statement
// itself, never accepted from this request — the same rule status_pembayaran
// follows.
func (c *StokOpnameUseCase) UpdateDetail(ctx context.Context, request *model.UpdateStokOpnameDetailRequest) (*model.StokOpnameResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if !request.StokSO.Present && !request.Keterangan.Present {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDraft); err != nil {
		return nil, err
	}

	err = c.StokOpnameRepository.UpdateDetailHitung(
		ctx, tx, request.ID, request.IDDetail, request.ActorID,
		request.StokSO.Present, request.StokSO.Value,
		request.Keterangan.Present, request.Keterangan.Value,
	)
	if err != nil {
		return nil, notFoundOnNoRows(err, "baris stok_opname not found")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Ajukan hands a draft count to the verifier. Refused when no line has been
// counted at all — an opname with nothing counted is an empty document, not a
// count. A partial count (one shelf, one category) is otherwise a legitimate way
// to work, and the response reports how many lines are still outstanding rather
// than blocking on them, so the verifier decides with open eyes.
func (c *StokOpnameUseCase) Ajukan(ctx context.Context, request *model.AjukanStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDraft); err != nil {
		return nil, err
	}

	total, belumDihitung, err := c.StokOpnameRepository.CountBelumDihitung(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}
	if total == belumDihitung {
		return nil, model.Invalid("stok_opname tanpa satu pun baris terhitung tidak bisa diajukan")
	}

	if err := c.StokOpnameRepository.Ajukan(ctx, tx, request.ID, time.Now()); err != nil {
		return nil, conflictOnTransisi(err, "status stok_opname sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Tolak sends a submitted count back to DRAFT for a recount. Unlike pemakaian's
// Tolak this is not a business refusal and not terminal: the room stays frozen
// either way, since DRAFT and DIAJUKAN are both frozen, so nothing about the
// freeze changes and no ruang: lock is needed here.
func (c *StokOpnameUseCase) Tolak(ctx context.Context, request *model.TolakStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	if _, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDiajukan); err != nil {
		return nil, err
	}

	if err := c.StokOpnameRepository.Tolak(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status stok_opname sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Posting writes the selisih between what was counted and what the system held
// at cutoff — never a value the balance is simply set to. See "Jebakan utama" in
// CLAUDE.md for why the two read as the same thing and are not: this re-verifies
// the room's current balance against the frozen stok_awal for every counted line,
// under the balance locks, and refuses outright if either has moved. Under an
// intact freeze that branch can never fire — and that is exactly why it has to
// exist: the only thing that could ever trip it is a bug in the freeze itself.
//
// Lines with StokSO nil (never counted) are skipped entirely, and so are lines
// whose selisih came out zero — kartu_stok must never carry a row that moved
// nothing. If every line is zero or uncounted, the document still posts with no
// adjustment rows at all: "nothing was wrong" is the best possible outcome of a
// count and has to be recordable, unlike pemakaian where "nothing left" means the
// request was pointless.
func (c *StokOpnameUseCase) Posting(ctx context.Context, request *model.PostingStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	opname, err := c.kunciDenganStatus(ctx, tx, request.ID, entity.StatusStokOpnameDiajukan)
	if err != nil {
		return nil, err
	}

	// The status guard is the primary defence; this one does not depend on the
	// status column being right.
	posted, err := c.KartuStokRepository.HasRef(ctx, tx, entity.RefTableStokOpname, request.ID)
	if err != nil {
		return nil, err
	}
	if posted {
		return nil, model.Conflict("stok_opname ini sudah punya baris kartu stok")
	}

	semuaDetail, err := c.StokOpnameRepository.FindDetail(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}

	dihitung := make([]entity.StokOpnameDetail, 0, len(semuaDetail))
	for i := range semuaDetail {
		if semuaDetail[i].StokSO != nil {
			dihitung = append(dihitung, semuaDetail[i])
		}
	}

	if err := c.kunciJalurStok(ctx, tx, opname, dihitung); err != nil {
		return nil, err
	}

	// Checked so the refusal can name the period; the trigger stays the guard.
	// Dated ts_cutoff, not today — the selisih is a fact about the shelf at that
	// instant, and monthly shrinkage reporting asks about the month counted, not
	// the month booked.
	if err := periksaPeriode(ctx, tx, c.PeriodeRepository, opname.TsCutoff); err != nil {
		return nil, err
	}

	if err := c.periksaSaldoBergeser(ctx, tx, opname, dihitung); err != nil {
		return nil, err
	}

	for i := range dihitung {
		baris := &dihitung[i]

		if baris.StokSelisihLebih == 0 && baris.StokSelisihKurang == 0 {
			continue
		}

		var kartu *entity.KartuStok

		if baris.StokSelisihLebih > 0 {
			// Surplus is the one incoming row in this whole codebase with no
			// counterparty transaction behind it — no invoice, no source room. It is
			// valued at the room's own moving average, read after the balance lock is
			// held, so goods that turn up are simply goods that were always on the
			// shelf: the average does not move at all.
			saldoSekarang, err := c.KartuStokRepository.SaldoTerakhir(ctx, tx, baris.IDBarang, opname.IDRuang)
			if err != nil {
				return nil, err
			}

			hpp := mustParseNumeric(saldoSekarang.HargaPokokSatuan)
			nilaiMasuk := new(big.Rat).Mul(hpp, ratFromInt(baris.StokSelisihLebih))

			kartu = &entity.KartuStok{
				IDBarang:         baris.IDBarang,
				IDRuang:          opname.IDRuang,
				TanggalTransaksi: opname.TsCutoff,
				JenisTransaksi:   entity.JenisTransaksiSOSurplus,
				StokMasuk:        baris.StokSelisihLebih,
				NilaiMasuk:       formatNumeric(roundNumeric(nilaiMasuk, skalaUang), skalaUang),
				RefTable:         entity.RefTableStokOpname,
				RefIDTransaksi:   opname.ID,
				CreatedBy:        request.ActorID,
			}
		} else {
			// Deficit is an ordinary outgoing row: the trigger overwrites
			// nilai_keluar and harga_pokok_satuan from the running average, the same
			// as mutasi's and pemakaian's outgoing halves.
			kartu = &entity.KartuStok{
				IDBarang:         baris.IDBarang,
				IDRuang:          opname.IDRuang,
				TanggalTransaksi: opname.TsCutoff,
				JenisTransaksi:   entity.JenisTransaksiSODefisit,
				StokKeluar:       baris.StokSelisihKurang,
				NilaiMasuk:       "0",
				RefTable:         entity.RefTableStokOpname,
				RefIDTransaksi:   opname.ID,
				CreatedBy:        request.ActorID,
			}
		}

		if err := c.KartuStokRepository.Insert(ctx, tx, kartu); err != nil {
			return nil, invalidOnCheck(
				err,
				fmt.Sprintf(
					"posting ditolak kartu stok: %s di ruang %d, periode sudah TUTUP atau saldo bergeser",
					baris.NamaProduct, opname.IDRuang,
				),
			)
		}

		if err := c.StokOpnameRepository.UpdateDetailPosting(ctx, tx, baris.ID, kartu.ID); err != nil {
			return nil, err
		}
	}

	if err := c.StokOpnameRepository.Posting(ctx, tx, request.ID, request.ActorID); err != nil {
		return nil, conflictOnTransisi(err, "status stok_opname sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// Batal voids a document from any non-BATAL status.
//
// From DRAFT/DIAJUKAN no kartu_stok row exists yet, so this only changes the
// status; it takes the exclusive ruang: lock itself, mirroring Create, since
// nothing here pre-locks a balance for KunciSaldo to protect.
//
// From POSTED it appends a reversing row for every adjustment the posting made,
// exactly like every other stock-writing module's cancellation, and takes the
// shared ruang: lock before its own balance locks instead, since it — like
// Posting — is now a module that pre-locks balances.
func (c *StokOpnameUseCase) Batal(ctx context.Context, request *model.BatalStokOpnameRequest) (*model.StokOpnameResponse, error) {
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

	opname, err := c.StokOpnameRepository.LockByID(ctx, tx, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "stok_opname not found")
	}

	if opname.Status == entity.StatusStokOpnamePosted {
		asal, err := c.KartuStokRepository.FindByRef(ctx, tx, entity.RefTableStokOpname, request.ID)
		if err != nil {
			return nil, err
		}

		tanggalPembalik := time.Now()

		if err := c.RuangRepository.LockShared(ctx, tx, opname.IDRuang); err != nil {
			return nil, err
		}

		keys := make([]repository.SaldoKey, 0, len(asal))
		for i := range asal {
			keys = append(keys, repository.SaldoKey{IDBarang: asal[i].IDBarang, IDRuang: asal[i].IDRuang})
		}

		if err := c.KartuStokRepository.KunciSaldo(ctx, tx, keys); err != nil {
			return nil, err
		}

		if err := periksaPeriode(ctx, tx, c.PeriodeRepository, tanggalPembalik); err != nil {
			return nil, err
		}

		for i := range asal {
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
				RefTable:         entity.RefTableStokOpname,
				RefIDTransaksi:   request.ID,
				IDKartuStokAsal:  &idAsal,
				Keterangan:       &keterangan,
				CreatedBy:        request.ActorID,
			}

			if err := c.KartuStokRepository.Insert(ctx, tx, pembalik); err != nil {
				return nil, invalidOnCheck(
					err,
					"pembatalan ditolak kartu stok: barang sudah bergerak lagi atau periode sudah TUTUP",
				)
			}
		}
	} else {
		if err := c.RuangRepository.Lock(ctx, tx, opname.IDRuang); err != nil {
			return nil, err
		}
	}

	if err := c.StokOpnameRepository.Batal(ctx, tx, request.ID, request.ActorID, request.AlasanBatal); err != nil {
		return nil, conflictOnTransisi(err, "status stok_opname sudah berubah")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, c.DB, request.ID, nil)
}

// kunciJalurStok takes every balance lock Posting's inserts will need, up front,
// in the canonical order every writer in the system uses — periode, then ruang,
// then (id_barang, id_ruang). The ruang: lock sits here, before KunciSaldo,
// following the same rule mutasi and pemakaian's posting paths now do: this
// module pre-locks balances, so periksaSaldoBergeser has to read a status that
// cannot move underneath it for the rest of the transaction.
func (c *StokOpnameUseCase) kunciJalurStok(ctx context.Context, tx repository.DBTX, opname *entity.StokOpname, dihitung []entity.StokOpnameDetail) error {
	if err := c.PeriodeRepository.LockShared(ctx, tx, opname.TsCutoff); err != nil {
		return err
	}

	if err := c.RuangRepository.LockShared(ctx, tx, opname.IDRuang); err != nil {
		return err
	}

	keys := make([]repository.SaldoKey, 0, len(dihitung))
	for i := range dihitung {
		keys = append(keys, repository.SaldoKey{IDBarang: dihitung[i].IDBarang, IDRuang: opname.IDRuang})
	}

	return c.KartuStokRepository.KunciSaldo(ctx, tx, keys)
}

// periksaSaldoBergeser is the assertion at the heart of this module: the room's
// balance right now, read under the balance lock, must equal stok_awal exactly —
// the figure frozen at TarikSaldo. Under an intact freeze it always will, because
// nothing else could have posted into this room since. If it does not, something
// bypassed the freeze, and the honest response is to refuse and say so rather
// than silently accept a selisih computed against a balance that has moved.
//
// This is not "set the balance to what was counted" — see "Jebakan utama" in
// CLAUDE.md for why that would erase whatever moved in between rather than
// explain it.
func (c *StokOpnameUseCase) periksaSaldoBergeser(ctx context.Context, tx repository.DBTX, opname *entity.StokOpname, dihitung []entity.StokOpnameDetail) error {
	if len(dihitung) == 0 {
		return nil
	}

	barangIDs := make([]int64, len(dihitung))
	ruangIDs := make([]int64, len(dihitung))

	for i := range dihitung {
		barangIDs[i] = dihitung[i].IDBarang
		ruangIDs[i] = opname.IDRuang
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, tx, barangIDs, ruangIDs)
	if err != nil {
		return err
	}

	for i := range dihitung {
		sekarang := saldo[repository.SaldoKey{IDBarang: dihitung[i].IDBarang, IDRuang: opname.IDRuang}].StokAkhir

		if sekarang != dihitung[i].StokAwal {
			return model.Conflict(fmt.Sprintf(
				"saldo %s di ruang %d telah bergeser sejak cutoff (saat itu %d, sekarang %d); "+
					"hitung ulang atau batalkan opname ini",
				dihitung[i].NamaProduct, opname.IDRuang, dihitung[i].StokAwal, sekarang,
			))
		}
	}

	return nil
}

// detail loads a header and its lines. Two queries, independent of how many
// lines come back.
func (c *StokOpnameUseCase) detail(ctx context.Context, db repository.DBTX, id int64, aktifIDUnitKerja *int64) (*model.StokOpnameResponse, error) {
	opname, err := c.StokOpnameRepository.FindByID(ctx, db, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "stok_opname not found")
	}

	// isu #12 fase 6.
	if diLuarUnitAktif(opname.IDUnitKerjaRuang, aktifIDUnitKerja) {
		return nil, model.NotFound("stok_opname not found")
	}

	// Non-nil even when empty, so the response carries [] rather than dropping
	// the key and implying the document was never asked about.
	opname.Detail, err = c.StokOpnameRepository.FindDetail(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return converter.StokOpnameToResponse(opname), nil
}

// kunciDenganStatus takes the row lock and refuses a document in the wrong
// state.
func (c *StokOpnameUseCase) kunciDenganStatus(ctx context.Context, tx repository.DBTX, id int64, diharapkan string) (*entity.StokOpname, error) {
	opname, err := c.StokOpnameRepository.LockByID(ctx, tx, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "stok_opname not found")
	}

	if opname.Status != diharapkan {
		return nil, model.Conflict(fmt.Sprintf(
			"aksi ini butuh status %s, stok_opname sekarang %s", diharapkan, opname.Status,
		))
	}

	return opname, nil
}
