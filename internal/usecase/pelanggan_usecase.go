package usecase

import (
	"context"
	"database/sql"
	"strings"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// PelangganUseCase holds the business rules for pelanggan. It owns the
// transaction boundary and returns models, never entities or Fiber types.
//
// PenjualanRepository is borrowed for GET /pelanggan/{id}/piutang — isu #10 fase 2,
// the same way SupplierUseCase borrows PembelianRepository for
// GET /supplier/{id}/utang: the query is over penjualan's own tables, so it stays in
// that module's repository, and only the resource it answers for is a customer's.
type PelangganUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	PelangganRepository *repository.PelangganRepository
	PenjualanRepository *repository.PenjualanRepository
}

func NewPelangganUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	pelangganRepository *repository.PelangganRepository,
	penjualanRepository *repository.PenjualanRepository,
) *PelangganUseCase {
	return &PelangganUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		PelangganRepository: pelangganRepository,
		PenjualanRepository: penjualanRepository,
	}
}

// plafonKreditIsNegative rejects a negative credit limit before it reaches the
// pelanggan_plafon_check constraint, which would surface as an opaque 500.
// Checking the sign as text keeps floats out of a money value entirely.
func plafonKreditIsNegative(plafon *string) bool {
	return plafon != nil && strings.HasPrefix(strings.TrimSpace(*plafon), "-")
}

func (c *PelangganUseCase) Create(ctx context.Context, request *model.CreatePelangganRequest) (*model.PelangganResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	if plafonKreditIsNegative(request.PlafonKredit) {
		return nil, model.Invalid("plafon_kredit cannot be negative")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback() // no-op once the transaction is committed
	}()

	// Kode is optional; uniqueness only matters when one is supplied. Several
	// customers may share kode = NULL, so checking a NULL would be pointless.
	if request.Kode != nil {
		exists, err := c.PelangganRepository.ExistsByKode(ctx, tx, *request.Kode, 0)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode pelanggan already used")
		}
	}

	pelanggan := &entity.Pelanggan{
		Kode:    request.Kode,
		Nama:    request.Nama,
		Telepon: request.Telepon,
		Alamat:  request.Alamat,
		NPWP:    request.NPWP,
		// Left nil when absent: no credit limit, which is not a limit of zero.
		PlafonKredit: request.PlafonKredit,
		IsAktif:      true,
		CreatedBy:    &request.ActorID,
	}

	if err := c.PelangganRepository.Create(ctx, tx, pelanggan); err != nil {
		return nil, conflictOnUnique(err, "kode pelanggan already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.PelangganToResponse(pelanggan), nil
}

func (c *PelangganUseCase) Get(ctx context.Context, request *model.GetPelangganRequest) (*model.PelangganResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	pelanggan, err := c.PelangganRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "pelanggan not found")
	}

	return converter.PelangganToResponse(pelanggan), nil
}

func (c *PelangganUseCase) Update(ctx context.Context, request *model.UpdatePelangganRequest) (*model.PelangganResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// nama and is_aktif are NOT NULL: changeable, never clearable. Sending null
	// for plafon_kredit, by contrast, is a real request — it lifts the limit.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	if plafonKreditIsNegative(request.PlafonKredit.Value) {
		return nil, model.Invalid("plafon_kredit cannot be negative")
	}

	patch := repository.PelangganPatch{
		SetKode:         request.Kode.Present,
		Kode:            request.Kode.Value,
		Nama:            request.Nama.Value,
		SetTelepon:      request.Telepon.Present,
		Telepon:         request.Telepon.Value,
		SetAlamat:       request.Alamat.Present,
		Alamat:          request.Alamat.Value,
		SetNPWP:         request.NPWP.Present,
		NPWP:            request.NPWP.Value,
		SetPlafonKredit: request.PlafonKredit.Present,
		PlafonKredit:    request.PlafonKredit.Value,
		IsAktif:         request.IsAktif.Value,
		SetUpdatedBy:    true,
		UpdatedBy:       &request.ActorID,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if !patch.SetKode && patch.Nama == nil && !patch.SetTelepon && !patch.SetAlamat &&
		!patch.SetNPWP && !patch.SetPlafonKredit && patch.IsAktif == nil {
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
		exists, err := c.PelangganRepository.ExistsByKode(ctx, tx, *patch.Kode, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode pelanggan already used")
		}
	}

	pelanggan, err := c.PelangganRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "kode pelanggan already used"), "pelanggan not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.PelangganToResponse(pelanggan), nil
}

func (c *PelangganUseCase) Search(ctx context.Context, request *model.ListPelangganRequest) ([]model.PelangganResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.PelangganRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PelangganToResponses(list), pageMetadata(&request.PageRequest, total), nil
}

// Piutang answers which of a customer's KREDIT notas are still open, and for how
// much — isu #10 fase 2, a read that is not a module, following
// SupplierUseCase.Utang.
//
// The customer is looked up first so an unknown id answers 404 rather than an empty
// page. Those are different facts — "this customer owes nothing" and "there is no
// such customer" — and a client that cannot tell them apart shows the wrong one.
func (c *PelangganUseCase) Piutang(ctx context.Context, request *model.ListPiutangPelangganRequest) ([]model.PiutangPelangganResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	if _, err := c.PelangganRepository.FindByID(ctx, c.DB, request.IDPelanggan); err != nil {
		return nil, nil, notFoundOnNoRows(err, "pelanggan not found")
	}

	list, total, err := c.PenjualanRepository.FindPiutangPelanggan(
		ctx, c.DB, request.IDPelanggan, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.PiutangPelangganToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
