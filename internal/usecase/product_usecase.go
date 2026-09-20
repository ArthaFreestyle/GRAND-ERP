package usecase

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// dateOnly parses berlaku_dari, which is a DATE: no time, no zone.
const dateOnly = "2006-01-02"

// tanggalHargaJual truncates a point in time to the calendar date that decides which
// product_harga_jual version applies, in the store's own calendar (WIB) rather than
// the server's or UTC's.
//
// This is the trap isu #8 calls out by name: a transaction at 00:30 WIB on the 15th
// is 17:30 UTC on the 14th. Truncating in UTC would price it against the 14th's
// version even though the till and the customer both agree it is the 15th. Today
// only GET .../harga-jual's default date goes through this — penjualan.tanggal is a
// TIMESTAMPTZ and will need the same conversion once that module exists, which is why
// this lives as its own function rather than inlined at the one call site that uses
// it so far.
//
// The returned time is midnight UTC on that date, which is how every DATE column in
// this codebase is already represented in Go (see berlakuDari in AddHargaJual).
func tanggalHargaJual(t time.Time) time.Time {
	tahun, bulan, hari := t.In(zonaWIB).Date()

	return time.Date(tahun, bulan, hari, 0, 0, 0, 0, time.UTC)
}

// ProductUseCase holds the business rules for product and its two child tables.
//
// It needs no SatuanRepository: a bad id_satuan arrives as a foreign-key violation,
// which invalidOnForeignKey turns into a 400 naming the field. A pre-check would be a
// second query that still could not close the race.
//
// It carries the rules the database cannot: the base unit must exist with faktor = 1,
// a price may only be set for a unit the product actually sells in, and opening a price
// version must close the previous one in the same transaction.
//
// PembelianRepository and KartuStokRepository are here for one read each, and neither
// makes this module own those tables: the purchase history behind
// GET /product/{id}/riwayat-beli and the per-room balance behind
// GET /product/{id}/stok. It is the same arrangement UserUseCase has with
// RoleRepository — the SQL is over another module's tables, so it stays in that
// module's repository, and the usecase that owns the resource borrows it. The
// alternative, a usecase apiece, would be a module for what is one query.
//
// RuangRepository is borrowed once more for the same reason — isu #11's
// GET /pos/product requires id_ruang and validates it names a real room before
// answering, the same existence check pembelian and mutasi already run before
// trusting the field for anything.
type ProductUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	ProductRepository   *repository.ProductRepository
	PembelianRepository *repository.PembelianRepository
	KartuStokRepository *repository.KartuStokRepository
	RuangRepository     *repository.RuangRepository

	// UnitKerjaRepository validates that a catalog membership names an active unit —
	// the foreign key cannot tell a retired unit from a live one.
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewProductUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	productRepository *repository.ProductRepository,
	pembelianRepository *repository.PembelianRepository,
	kartuStokRepository *repository.KartuStokRepository,
	ruangRepository *repository.RuangRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *ProductUseCase {
	return &ProductUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		ProductRepository:   productRepository,
		PembelianRepository: pembelianRepository,
		KartuStokRepository: kartuStokRepository,
		RuangRepository:     ruangRepository,
		UnitKerjaRepository: unitKerjaRepository,
	}
}

// Create writes the product and every conversion unit in one transaction.
//
// The base unit is registered automatically with faktor = 1, so a product can never end
// up without it. Relying on the caller to send it would make the invariant depend on
// discipline, and nothing in the schema enforces it.
func (c *ProductUseCase) Create(ctx context.Context, request *model.CreateProductRequest) (*model.ProductResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	berlakuFaktorSatu, err := c.mergeSatuan(request)
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

	exists, err := c.ProductRepository.ExistsByKodeBarang(ctx, tx, request.KodeBarang, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, model.Conflict("kode barang sudah dipakai")
	}

	unitKerjaAwal, err := c.unitKerjaAwal(ctx, tx, request.AktifIDUnitKerja)
	if err != nil {
		return nil, err
	}

	product := &entity.Product{
		KodeBarang:    request.KodeBarang,
		Nama:          request.Nama,
		IDSatuanDasar: request.IDSatuanDasar,
		StokMinimum:   request.StokMinimum,
		IsAktif:       true,
		CreatedBy:     request.ActorID,
	}

	if err := c.ProductRepository.Create(ctx, tx, product); err != nil {
		// A bad id_satuan_dasar arrives as a foreign-key violation. Answering 400
		// names the caller's mistake instead of hiding it behind a 500.
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "kode barang sudah dipakai"),
			"id_satuan_dasar tidak ada di tabel satuan",
		)
	}

	for i := range berlakuFaktorSatu {
		berlakuFaktorSatu[i].IDProduct = product.ID

		if err := c.ProductRepository.InsertSatuan(ctx, tx, &berlakuFaktorSatu[i]); err != nil {
			return nil, invalidOnForeignKey(
				conflictOnUnique(err, "hanya boleh satu satuan default per produk"),
				"id_satuan tidak ada di tabel satuan",
			)
		}
	}

	actor := request.ActorID
	if err := c.ProductRepository.InsertUnitKerja(ctx, tx, product.ID, unitKerjaAwal, &actor); err != nil {
		return nil, invalidOnForeignKey(err, "id_unit_kerja tidak ada di tabel unit_kerja")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the base unit's name and the stored rows rather
	// than what was sent.
	return c.detail(ctx, product.ID)
}

// unitKerjaAwal resolves the catalog a new product starts in from the caller's active
// unit_kerja, never from the body: a session active in a unit starts the product in that
// unit only, and a global session (aktif == nil — SUPERADMIN, or no active context) starts
// it in every ACTIVE unit, the same default migration 000029 gave the products that
// already existed.
//
// A scoped start is the point of a per-unit catalog: what INVENTARIS of one outlet adds is
// not silently sold at every other outlet. Widening later is PUT /product/{id}/unit-kerja.
// The session's unit is checked active because a grant embedded in a token outlives the
// unit being retired; the foreign key alone would accept it.
func (c *ProductUseCase) unitKerjaAwal(ctx context.Context, tx repository.DBTX, aktif *int64) ([]int64, error) {
	if aktif == nil {
		return c.ProductRepository.IDUnitKerjaAktifSemua(ctx, tx)
	}

	ada, err := c.UnitKerjaRepository.CountActiveByIDs(ctx, tx, []int64{*aktif})
	if err != nil {
		return nil, err
	}
	if ada != 1 {
		return nil, model.Invalid("unit kerja aktif sesi ini sudah nonaktif; pilih konteks lain lewat switch-context")
	}

	return []int64{*aktif}, nil
}

// uniqueInt64 keeps the first occurrence of each id, in order. CountActiveByIDs compares
// a count against the number of ids, so a duplicate would wrongly reject a valid request.
func uniqueInt64(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	unik := make([]int64, 0, len(ids))

	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}
		unik = append(unik, id)
	}

	return unik
}

// SetUnitKerja replaces the set of unit catalogs a product is in.
//
// Taking a product OUT of a unit is refused 409 while any room of that unit still holds
// it (stok_akhir > 0) — the same shape as retiring a ruang that still holds stock. Stock
// no catalog admits could not be sold, counted, or moved by any document, and would still
// sit on the inventory value report. Empty the unit's rooms with a mutasi or a pemakaian
// first.
//
// The order inside the transaction is the guarantee, not a style: the membership rows are
// DELETEd first and the stock read afterwards. A posting in flight holds those rows FOR
// SHARE (periksaKatalogRuang), so the DELETE waits until it has committed, and the stock
// check then sees what it wrote. Reading the stock first would leave a window in which a
// posting lands between the two.
func (c *ProductUseCase) SetUnitKerja(ctx context.Context, request *model.SetProductUnitKerjaRequest) (*model.ProductResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	if _, err := c.ProductRepository.FindByID(ctx, tx, request.IDProduct); err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	ids := uniqueInt64(request.IDUnitKerja)

	sekarang, err := c.ProductRepository.FindUnitKerja(ctx, tx, request.IDProduct)
	if err != nil {
		return nil, err
	}

	sudahAda := make(map[int64]struct{}, len(sekarang))
	for i := range sekarang {
		sudahAda[sekarang[i].IDUnitKerja] = struct{}{}
	}

	// Only a NEW membership has to name an active unit. Echoing back a retired unit the
	// product already sits in is how a client keeps it, not a request to add it.
	baru := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := sudahAda[id]; !ok {
			baru = append(baru, id)
		}
	}

	if len(baru) > 0 {
		aktif, err := c.UnitKerjaRepository.CountActiveByIDs(ctx, tx, baru)
		if err != nil {
			return nil, err
		}
		if aktif != int64(len(baru)) {
			return nil, model.Invalid("id_unit_kerja harus unit kerja yang ada dan aktif")
		}
	}

	dicabut, err := c.ProductRepository.DeleteUnitKerjaSelain(ctx, tx, request.IDProduct, ids)
	if err != nil {
		return nil, err
	}

	masihAdaStok, err := c.KartuStokRepository.UnitDenganSaldoPositif(ctx, tx, request.IDProduct, dicabut)
	if err != nil {
		return nil, err
	}
	if len(masihAdaStok) > 0 {
		return nil, model.Conflict(fmt.Sprintf(
			"produk masih punya stok di unit kerja id %s — kosongkan dulu dengan mutasi atau pemakaian",
			joinIDs(masihAdaStok),
		))
	}

	actor := request.ActorID
	if err := c.ProductRepository.InsertUnitKerja(ctx, tx, request.IDProduct, baru, &actor); err != nil {
		return nil, invalidOnForeignKey(err, "id_unit_kerja tidak ada di tabel unit_kerja")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.IDProduct)
}

// joinIDs renders ids as "1, 2, 3" for an error message.
func joinIDs(ids []int64) string {
	bagian := make([]string, len(ids))
	for i, id := range ids {
		bagian[i] = strconv.FormatInt(id, 10)
	}

	return strings.Join(bagian, ", ")
}

// mergeSatuan builds the rows to insert: the base unit at faktor = 1, plus whatever the
// caller asked for.
//
// A caller who lists the base unit again is not making an error — it is the obvious
// thing to do — so that entry collapses into the automatic row instead of colliding.
// Its faktor is forced back to 1 whatever was sent, because the base unit is one base
// unit by definition and any other value would corrupt every conversion built on it.
func (c *ProductUseCase) mergeSatuan(request *model.CreateProductRequest) ([]entity.ProductSatuan, error) {
	rows := []entity.ProductSatuan{{
		IDSatuan: request.IDSatuanDasar,
		Faktor:   1,
	}}

	seen := map[int64]int{request.IDSatuanDasar: 0}

	for _, satuan := range request.Satuan {
		if index, duplicate := seen[satuan.IDSatuan]; duplicate {
			if satuan.IDSatuan == request.IDSatuanDasar && satuan.Faktor != 1 {
				return nil, model.Invalid("faktor satuan dasar harus 1")
			}

			// A repeat of a non-base unit with a different factor is ambiguous, so it
			// is rejected rather than silently resolved to the last one written.
			if rows[index].Faktor != satuan.Faktor {
				return nil, model.Invalid("satuan yang sama dikirim dua kali dengan faktor berbeda")
			}

			rows[index].IsDefaultInput = rows[index].IsDefaultInput || satuan.IsDefaultInput

			continue
		}

		seen[satuan.IDSatuan] = len(rows)
		rows = append(rows, entity.ProductSatuan{
			IDSatuan:       satuan.IDSatuan,
			Faktor:         satuan.Faktor,
			IsDefaultInput: satuan.IsDefaultInput,
		})
	}

	// product_satuan_default_uidx permits one flagged row per product, so more than
	// one here would fail at the database with a message naming an index. Catching it
	// first says what the caller actually got wrong.
	defaults := 0
	for i := range rows {
		if rows[i].IsDefaultInput {
			defaults++
		}
	}

	if defaults > 1 {
		return nil, model.Invalid("hanya boleh satu satuan dengan is_default_input")
	}

	return rows, nil
}

// Get returns a product with its units and price history.
func (c *ProductUseCase) Get(ctx context.Context, request *model.GetProductRequest) (*model.ProductResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.ID)
}

// detail loads a product and its three children (units, prices, catalog membership).
// Four queries, independent of how many rows come back.
func (c *ProductUseCase) detail(ctx context.Context, id int64) (*model.ProductResponse, error) {
	product, err := c.ProductRepository.FindByID(ctx, c.DB, id)
	if err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	satuan, err := c.ProductRepository.FindSatuan(ctx, c.DB, id)
	if err != nil {
		return nil, err
	}

	hargaJual, err := c.ProductRepository.FindHargaJual(ctx, c.DB, id)
	if err != nil {
		return nil, err
	}

	// Non-nil even when empty, so the response carries [] rather than dropping the key
	// and implying the product was never asked about.
	unitKerja, err := c.ProductRepository.FindUnitKerja(ctx, c.DB, id)
	if err != nil {
		return nil, err
	}

	product.Satuan = satuan
	product.HargaJual = hargaJual
	product.UnitKerja = unitKerja

	return converter.ProductToResponse(product), nil
}

func (c *ProductUseCase) Update(ctx context.Context, request *model.UpdateProductRequest) (*model.ProductResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// All three columns are NOT NULL: changeable, never clearable.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.StokMinimum.Clears() {
		return nil, model.Invalid("stok_minimum cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.ProductPatch{
		Nama:        request.Nama.Value,
		StokMinimum: request.StokMinimum.Value,
		IsAktif:     request.IsAktif.Value,
		UpdatedBy:   &request.ActorID,
	}

	// An empty body would still fire the updated_at trigger, recording a change that
	// never happened.
	if patch.Nama == nil && patch.StokMinimum == nil && patch.IsAktif == nil {
		return nil, model.Invalid("no fields to update")
	}

	product, err := c.ProductRepository.Update(ctx, c.DB, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	return c.detail(ctx, product.ID)
}

// AddSatuan registers one more conversion unit on an existing product.
func (c *ProductUseCase) AddSatuan(ctx context.Context, request *model.AddProductSatuanRequest) (*model.ProductResponse, error) {
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

	product, err := c.ProductRepository.FindByID(ctx, tx, request.IDProduct)
	if err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	// The base unit is one base unit, and every other faktor is expressed against it.
	if request.IDSatuan == product.IDSatuanDasar && request.Faktor != 1 {
		return nil, model.Invalid("faktor satuan dasar harus 1")
	}

	// Moving the default is a two-step operation: only one row per product may carry
	// the flag, so the old holder has to be cleared before the new one claims it.
	// Inside the transaction, so a failure cannot leave a product with no default at
	// all.
	if request.IsDefaultInput {
		if err := c.ProductRepository.ClearDefaultSatuan(ctx, tx, request.IDProduct, request.IDSatuan); err != nil {
			return nil, err
		}
	}

	satuan := &entity.ProductSatuan{
		IDProduct:      request.IDProduct,
		IDSatuan:       request.IDSatuan,
		Faktor:         request.Faktor,
		IsDefaultInput: request.IsDefaultInput,
	}

	if err := c.ProductRepository.InsertSatuan(ctx, tx, satuan); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnUnique(err, "hanya boleh satu satuan default per produk"),
			"id_satuan tidak ada di tabel satuan",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.IDProduct)
}

// AddHargaJual opens a new price version and closes the previous one, in one
// transaction.
//
// Both halves or neither: closing without opening leaves the product with no price,
// and opening without closing leaves two versions overlapping — which the exclusion
// constraint would reject anyway, so the write would fail with the old version already
// closed if these were separate transactions.
func (c *ProductUseCase) AddHargaJual(ctx context.Context, request *model.AddProductHargaJualRequest) (*model.ProductResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// The datetime tag already proved the layout; this converts it.
	berlakuDari, err := time.Parse(dateOnly, request.BerlakuDari)
	if err != nil {
		return nil, model.Invalid("berlaku_dari harus tanggal YYYY-MM-DD")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := c.ProductRepository.FindByID(ctx, tx, request.IDProduct); err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	// Nothing in the schema ties product_harga_jual.id_satuan to product_satuan, so a
	// price could otherwise be set for a unit the product is not sold in — a price that
	// no input screen would ever offer.
	hasSatuan, err := c.ProductRepository.HasSatuan(ctx, tx, request.IDProduct, request.IDSatuan)
	if err != nil {
		return nil, err
	}
	if !hasSatuan {
		return nil, model.Invalid("satuan itu belum terdaftar di product_satuan")
	}

	if err := c.ProductRepository.CloseOpenHargaJual(
		ctx, tx, request.IDProduct, request.IDSatuan, berlakuDari,
	); err != nil {
		return nil, err
	}

	harga := &entity.ProductHargaJual{
		IDProduct:   request.IDProduct,
		IDSatuan:    request.IDSatuan,
		Harga:       request.Harga,
		BerlakuDari: berlakuDari,
		CreatedBy:   request.ActorID,
	}

	if err := c.ProductRepository.InsertHargaJual(ctx, tx, harga); err != nil {
		return nil, invalidOnForeignKey(
			conflictOnExclusion(err, "periode harga tumpang tindih"),
			"id_satuan tidak ada di tabel satuan",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.IDProduct)
}

// HargaBerlaku answers which price version applies, per satuan, on one date — isu #8
// fase 1. This is the piece that actually unblocks penjualan: penjualan_detail.id_harga_jual
// needs a version, not a number, and until this existed the only way to find one was
// GET /product/{id} handing back the entire history and leaving "which one is current"
// to the client.
//
// The product is looked up first so an unknown id answers 404 rather than an empty
// list — "this product does not exist" and "this product has no price yet" are
// different facts, same argument as riwayat_beli.
func (c *ProductUseCase) HargaBerlaku(ctx context.Context, request *model.ListHargaJualBerlakuRequest) ([]model.HargaJualBerlakuResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	tanggal := tanggalHargaJual(time.Now())

	if request.Tanggal != "" {
		parsed, err := time.Parse(dateOnly, request.Tanggal)
		if err != nil {
			return nil, model.Invalid("tanggal harus tanggal YYYY-MM-DD")
		}

		tanggal = parsed
	}

	if _, err := c.ProductRepository.FindByID(ctx, c.DB, request.IDProduct); err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	list, err := c.ProductRepository.FindHargaBerlaku(ctx, c.DB, request.IDProduct, tanggal)
	if err != nil {
		return nil, err
	}

	return converter.HargaJualBerlakuToResponses(list), nil
}

// UpdateHargaJual corrects the price on an existing version — isu #8 fase 2.
//
// Refused once a penjualan_detail line references it: the master row would then no
// longer describe the version that document actually charged from, and that is the
// only trace left of which price-list entry a past sale came from. Wrapped in a
// transaction because the check and the write must see the same row — a version
// could be claimed by a document between the two otherwise.
func (c *ProductUseCase) UpdateHargaJual(ctx context.Context, request *model.UpdateProductHargaJualRequest) (*model.ProductResponse, error) {
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

	dipakai, err := c.ProductRepository.HargaJualDipakaiDokumen(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}
	if dipakai {
		return nil, model.Conflict("versi harga ini sudah dipakai dokumen penjualan")
	}

	if _, err := c.ProductRepository.UpdateHargaJual(ctx, tx, request.ID, request.IDProduct, request.Harga); err != nil {
		return nil, notFoundOnNoRows(err, "harga jual not found")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.IDProduct)
}

// DeleteHargaJual removes a price version outright and reopens whatever version came
// before it — isu #8 fase 2. Both writes are one transaction, the same rule
// AddHargaJual follows in the other direction: closing without opening, or here
// deleting without reopening, both leave the product with a date range no price
// covers.
//
// FindHargaJualByID takes FOR UPDATE first so the row cannot change shape between
// reading what to hand back to the previous version and actually deleting it.
func (c *ProductUseCase) DeleteHargaJual(ctx context.Context, request *model.DeleteProductHargaJualRequest) (*model.ProductResponse, error) {
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

	harga, err := c.ProductRepository.FindHargaJualByID(ctx, tx, request.ID, request.IDProduct)
	if err != nil {
		return nil, notFoundOnNoRows(err, "harga jual not found")
	}

	dipakai, err := c.ProductRepository.HargaJualDipakaiDokumen(ctx, tx, request.ID)
	if err != nil {
		return nil, err
	}
	if dipakai {
		return nil, model.Conflict("versi harga ini sudah dipakai dokumen penjualan")
	}

	if err := c.ProductRepository.DeleteHargaJual(ctx, tx, request.ID, request.IDProduct); err != nil {
		return nil, notFoundOnNoRows(err, "harga jual not found")
	}

	if err := c.ProductRepository.ReopenPreviousHargaJual(
		ctx, tx, request.IDProduct, harga.IDSatuan, harga.BerlakuDari, harga.BerlakuSampai,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return c.detail(ctx, request.IDProduct)
}

// DaftarHargaJual lists the price in force across every product, one row per product
// and satuan — isu #8 fase 3. This is what gets printed as a price list, and it is
// also the only way to find a product with no price registered at all without opening
// products one at a time.
func (c *ProductUseCase) DaftarHargaJual(ctx context.Context, request *model.ListDaftarHargaJualRequest) ([]model.DaftarHargaJualResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	tanggal := tanggalHargaJual(time.Now())

	if request.Tanggal != "" {
		parsed, err := time.Parse(dateOnly, request.Tanggal)
		if err != nil {
			return nil, nil, model.Invalid("tanggal harus tanggal YYYY-MM-DD")
		}

		tanggal = parsed
	}

	list, total, err := c.ProductRepository.SearchDaftarHargaJual(
		ctx, c.DB, request.Search, request.IsAktif, tanggal, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.DaftarHargaJualToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// RiwayatBeli answers what each supplier last charged for this product.
//
// This is isu #4 fase 4, and the point of it is that there is no purchase order in
// this system: what an operator actually wants before ordering is not a document to
// fill in but the price they last paid, and every POSTED purchase already recorded
// one. Nothing has to be entered for this to work, and nothing can fall out of step
// with it.
//
// The product is looked up first so an unknown id answers 404 rather than an empty
// page. Those are different facts — "nobody has ever bought this" and "there is no
// such product" — and a client that cannot tell them apart will show the wrong one.
func (c *ProductUseCase) RiwayatBeli(ctx context.Context, request *model.ListRiwayatBeliRequest) ([]model.RiwayatBeliResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	if _, err := c.ProductRepository.FindByID(ctx, c.DB, request.IDProduct); err != nil {
		return nil, nil, notFoundOnNoRows(err, "product not found")
	}

	list, total, err := c.PembelianRepository.FindRiwayatBeli(
		ctx, c.DB, request.IDProduct, request.IDSupplier, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.RiwayatBeliToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Stok answers where this product's stock currently sits, one row per room.
//
// The first read of kartu_stok in the codebase. Until now nothing needed one: purchases
// and follow-up receipts only add, and a return takes its quota from an invoice line.
// The mutasi entry screen cannot be used without it — nobody can choose a source room
// without knowing what is in it — and penjualan, pemakaian, and stok opname will all
// ask the same question.
//
// It is a read and not a guard, and the distinction is the whole point. What it returns
// may be stale before the caller acts on it; the kartu_stok trigger decides the balance
// under an advisory lock precisely so no reader can get in front of it. This is for the
// screen and for a message naming the shortfall, never for a decision.
//
// The product is looked up first so an unknown id answers 404 rather than an empty list.
// "Nobody has ever stocked this" and "there is no such product" are different facts, and
// a client that cannot tell them apart shows the wrong message.
func (c *ProductUseCase) Stok(ctx context.Context, request *model.ListStokProductRequest) ([]model.StokRuangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if _, err := c.ProductRepository.FindByID(ctx, c.DB, request.IDProduct); err != nil {
		return nil, notFoundOnNoRows(err, "product not found")
	}

	list, err := c.KartuStokRepository.SaldoPerRuang(ctx, c.DB, request.IDProduct, request.AktifIDUnitKerja)
	if err != nil {
		return nil, err
	}

	return converter.StokRuangToResponses(list), nil
}

// KartuStok answers one product's movement history in one room, oldest first — isu
// #22 fase 1, the first read of kartu_stok as a ledger rather than only a running
// balance. This is what answers "why is the balance what it is", which Stok above
// cannot: it shows the tail of the chain, not how it got there.
//
// The product is looked up first so an unknown id answers 404 rather than an empty
// page — the same argument RiwayatBeli and Stok both already make. The room is
// checked next, the same way: an id_ruang that names no room at all is a genuine
// 404, distinct from one that names a real room outside the caller's active
// unit_kerja, which the repository read itself answers with a silently empty page
// (isu #12 fase 6) rather than an error.
func (c *ProductUseCase) KartuStok(ctx context.Context, request *model.ListKartuStokRequest) ([]model.KartuStokResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	if _, err := c.ProductRepository.FindByID(ctx, c.DB, request.IDProduct); err != nil {
		return nil, nil, notFoundOnNoRows(err, "product not found")
	}

	if _, err := c.RuangRepository.FindByID(ctx, c.DB, request.IDRuang); err != nil {
		return nil, nil, notFoundOnNoRows(err, "ruang not found")
	}

	list, total, err := c.KartuStokRepository.Riwayat(
		ctx, c.DB, request.IDProduct, request.IDRuang, request.Dari, request.Sampai,
		request.AktifIDUnitKerja, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.RiwayatKartuStokToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// StokMinimum lists active products whose current stock has reached or fallen below
// their own stok_minimum — isu #22 fase 2, the natural pair to RiwayatBeli: that
// answers who to buy from and at what price, this answers what needs buying at all.
//
// Two queries regardless of how many products land on the page: StokMinimum for the
// page itself, and SaldoPerRuangBatch for every flagged product's per-room breakdown
// in one shot — the same anti-N+1 shape POS already established for a page of
// products wanting a per-row balance.
func (c *ProductUseCase) StokMinimum(ctx context.Context, request *model.ListStokMinimumRequest) ([]model.StokMinimumResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	var idRuang *int64
	if request.IDRuang > 0 {
		idRuang = &request.IDRuang
	}

	list, total, err := c.KartuStokRepository.StokMinimum(
		ctx, c.DB, request.Search, idRuang, request.AktifIDUnitKerja, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	paging := pageMetadata(&request.PageRequest, total)

	if len(list) == 0 {
		return converter.StokMinimumToResponses(list, nil), paging, nil
	}

	productIDs := make([]int64, len(list))
	for i := range list {
		productIDs[i] = list[i].IDProduct
	}

	perRuang, err := c.KartuStokRepository.SaldoPerRuangBatch(ctx, c.DB, productIDs, idRuang, request.AktifIDUnitKerja)
	if err != nil {
		return nil, nil, err
	}

	return converter.StokMinimumToResponses(list, perRuang), paging, nil
}

func (c *ProductUseCase) Search(ctx context.Context, request *model.ListProductRequest) ([]model.ProductResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.ProductRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.ProductToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// POS answers the POS catalog screen — isu #11: one nested read replacing what today
// costs one GET /product per row plus a GET .../harga-jual and a GET .../stok on top
// of that, on the single most frequently opened screen in the whole application.
//
// Exactly three queries once id_ruang is proven real, independent of how many rows
// the page holds: SearchPOS for the page of products, FindSatuanHargaBatch for every
// unit and its price on this page in one shot, and KartuStokRepository.SaldoBatch for
// every (product, room) balance on this page in one shot. Fetching any of those per
// row would defeat the entire reason this endpoint exists while still appearing to
// work.
func (c *ProductUseCase) POS(ctx context.Context, request *model.ListPosProductRequest) ([]model.PosProductResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	// A mistyped id_ruang that silently answered zero stock on every row would be an
	// expensive bug to notice, so it is checked before anything else runs.
	//
	// The unit it answers is also what narrows the catalog: the room's own unit, never
	// the caller's active one, matching periksaKatalogRuang — the screen must offer
	// exactly what the nota would then accept.
	idUnitKerja, err := c.RuangRepository.IDUnitKerjaByID(ctx, c.DB, request.IDRuang)
	if err != nil {
		return nil, nil, notFoundOnNoRows(err, "ruang not found")
	}

	tanggal := tanggalHargaJual(time.Now())

	if request.Tanggal != "" {
		parsed, err := time.Parse(dateOnly, request.Tanggal)
		if err != nil {
			return nil, nil, model.Invalid("tanggal harus tanggal YYYY-MM-DD")
		}

		tanggal = parsed
	}

	list, total, err := c.ProductRepository.SearchPOS(ctx, c.DB, request.Search, idUnitKerja, request.Size, request.Offset())
	if err != nil {
		return nil, nil, err
	}

	paging := pageMetadata(&request.PageRequest, total)

	if len(list) == 0 {
		return converter.PosProductToResponses(list), paging, nil
	}

	productIDs := make([]int64, len(list))
	ruangIDs := make([]int64, len(list))

	for i := range list {
		productIDs[i] = list[i].ID
		ruangIDs[i] = request.IDRuang
	}

	satuan, err := c.ProductRepository.FindSatuanHargaBatch(ctx, c.DB, productIDs, tanggal)
	if err != nil {
		return nil, nil, err
	}

	saldo, err := c.KartuStokRepository.SaldoBatch(ctx, c.DB, productIDs, ruangIDs)
	if err != nil {
		return nil, nil, err
	}

	for i := range list {
		// Never nil: every product carries at least its base unit, guaranteed at
		// Create.
		list[i].Satuan = satuan[list[i].ID]

		// A pair absent from the batch has never moved through this room — a zero
		// balance, not a missing one, the same reading SaldoBatch documents.
		if baris, ada := saldo[repository.SaldoKey{IDBarang: list[i].ID, IDRuang: request.IDRuang}]; ada {
			list[i].StokAkhir = baris.StokAkhir
		}
	}

	return converter.PosProductToResponses(list), paging, nil
}
