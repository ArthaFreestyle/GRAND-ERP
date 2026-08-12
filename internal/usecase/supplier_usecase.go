package usecase

import (
	"context"
	"database/sql"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// SupplierUseCase holds the business rules for supplier. It owns the transaction
// boundary and returns models, never entities or Fiber types.
type SupplierUseCase struct {
	DB                 *sql.DB
	Log                *logrus.Logger
	Validate           *validator.Validate
	SupplierRepository *repository.SupplierRepository
	// PembelianRepository is borrowed for GET /supplier/{id}/utang, the same way
	// ProductUseCase borrows it for riwayat-beli: the query is over pembelian's tables, so
	// it stays in that module's repository, and only the resource it answers for is a
	// supplier's.
	PembelianRepository *repository.PembelianRepository
}

func NewSupplierUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	supplierRepository *repository.SupplierRepository,
	pembelianRepository *repository.PembelianRepository,
) *SupplierUseCase {
	return &SupplierUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		SupplierRepository:  supplierRepository,
		PembelianRepository: pembelianRepository,
	}
}

func (c *SupplierUseCase) Create(ctx context.Context, request *model.CreateSupplierRequest) (*model.SupplierResponse, error) {
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

	// Kode is optional; uniqueness only matters when one is supplied. Several
	// suppliers may share kode = NULL, so checking a NULL would be pointless.
	if request.Kode != nil {
		exists, err := c.SupplierRepository.ExistsByKode(ctx, tx, *request.Kode, 0)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode supplier already used")
		}
	}

	supplier := &entity.Supplier{
		Kode:    request.Kode,
		Nama:    request.Nama,
		Telepon: request.Telepon,
		Alamat:  request.Alamat,
		NPWP:    request.NPWP,
		IsAktif: true,
		// created_by stays NULL until the auth module can supply the actor.
	}

	if err := c.SupplierRepository.Create(ctx, tx, supplier); err != nil {
		return nil, conflictOnUnique(err, "kode supplier already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.SupplierToResponse(supplier), nil
}

func (c *SupplierUseCase) Get(ctx context.Context, request *model.GetSupplierRequest) (*model.SupplierResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	supplier, err := c.SupplierRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "supplier not found")
	}

	return converter.SupplierToResponse(supplier), nil
}

func (c *SupplierUseCase) Update(ctx context.Context, request *model.UpdateSupplierRequest) (*model.SupplierResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// nama and is_aktif are NOT NULL: changeable, never clearable. Every other
	// field may legitimately be set to null.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.SupplierPatch{
		SetKode:    request.Kode.Present,
		Kode:       request.Kode.Value,
		Nama:       request.Nama.Value,
		SetTelepon: request.Telepon.Present,
		Telepon:    request.Telepon.Value,
		SetAlamat:  request.Alamat.Present,
		Alamat:     request.Alamat.Value,
		SetNPWP:    request.NPWP.Present,
		NPWP:       request.NPWP.Value,
		IsAktif:    request.IsAktif.Value,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if !patch.SetKode && patch.Nama == nil && !patch.SetTelepon &&
		!patch.SetAlamat && !patch.SetNPWP && patch.IsAktif == nil {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Only a non-null kode can collide; clearing it never can.
	if patch.Kode != nil {
		exists, err := c.SupplierRepository.ExistsByKode(ctx, tx, *patch.Kode, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode supplier already used")
		}
	}

	supplier, err := c.SupplierRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "kode supplier already used"), "supplier not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.SupplierToResponse(supplier), nil
}

func (c *SupplierUseCase) Search(ctx context.Context, request *model.ListSupplierRequest) ([]model.SupplierResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.SupplierRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.SupplierToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Utang answers which of a supplier's invoices are still open, and for how much.
//
// This is isu #4 fase 6, and like riwayat-beli it is a read rather than a module: no table,
// no migration, nothing to keep in step. The working list for deciding what to pay is
// entirely derivable from documents that were posted anyway — the invoice total, the
// payments effectively made against it, and the credits its returns produced.
//
// The supplier is looked up first so an unknown id answers 404 rather than an empty page.
// Those are different facts — "this supplier owes nothing" and "there is no such supplier" —
// and a client that cannot tell them apart will show the wrong one.
func (c *SupplierUseCase) Utang(ctx context.Context, request *model.ListUtangSupplierRequest) ([]model.UtangSupplierResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	if _, err := c.SupplierRepository.FindByID(ctx, c.DB, request.IDSupplier); err != nil {
		return nil, nil, notFoundOnNoRows(err, "supplier not found")
	}

	list, total, err := c.PembelianRepository.FindUtangSupplier(
		ctx, c.DB, request.IDSupplier, request.TermasukLunas, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.UtangSupplierToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
