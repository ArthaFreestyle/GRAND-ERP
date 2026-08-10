package usecase

import (
	"context"
	"database/sql"
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

// ProductUseCase holds the business rules for product and its two child tables.
//
// It needs no SatuanRepository: a bad id_satuan arrives as a foreign-key violation,
// which invalidOnForeignKey turns into a 400 naming the field. A pre-check would be a
// second query that still could not close the race.
//
// It carries the rules the database cannot: the base unit must exist with faktor = 1,
// a price may only be set for a unit the product actually sells in, and opening a price
// version must close the previous one in the same transaction.
type ProductUseCase struct {
	DB                *sql.DB
	Log               *logrus.Logger
	Validate          *validator.Validate
	ProductRepository *repository.ProductRepository
}

func NewProductUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	productRepository *repository.ProductRepository,
) *ProductUseCase {
	return &ProductUseCase{
		DB:                db,
		Log:               log,
		Validate:          validate,
		ProductRepository: productRepository,
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

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Re-read so the response carries the base unit's name and the stored rows rather
	// than what was sent.
	return c.detail(ctx, product.ID)
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

// detail loads a product and both children. Three queries, independent of how many rows
// come back.
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
	product.Satuan = satuan
	product.HargaJual = hargaJual

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
