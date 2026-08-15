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

// UnitKerjaUseCase holds the business rules for unit_kerja. It owns the
// transaction boundary and returns models, never entities or Fiber types.
type UnitKerjaUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewUnitKerjaUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *UnitKerjaUseCase {
	return &UnitKerjaUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		UnitKerjaRepository: unitKerjaRepository,
	}
}

func (c *UnitKerjaUseCase) Create(ctx context.Context, request *model.CreateUnitKerjaRequest) (*model.UnitKerjaResponse, error) {
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
	// units may share kode = NULL, so checking a NULL would be pointless.
	if request.Kode != nil {
		exists, err := c.UnitKerjaRepository.ExistsByKode(ctx, tx, *request.Kode, 0)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode unit kerja already used")
		}
	}

	unitKerja := &entity.UnitKerja{
		Kode:    request.Kode,
		Nama:    request.Nama,
		IsAktif: true,
		// created_by stays NULL until the caller can supply the actor.
	}

	if err := c.UnitKerjaRepository.Create(ctx, tx, unitKerja); err != nil {
		return nil, conflictOnUnique(err, "kode unit kerja already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.UnitKerjaToResponse(unitKerja), nil
}

func (c *UnitKerjaUseCase) Get(ctx context.Context, request *model.GetUnitKerjaRequest) (*model.UnitKerjaResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	unitKerja, err := c.UnitKerjaRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "unit kerja not found")
	}

	return converter.UnitKerjaToResponse(unitKerja), nil
}

func (c *UnitKerjaUseCase) Update(ctx context.Context, request *model.UpdateUnitKerjaRequest) (*model.UnitKerjaResponse, error) {
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

	patch := repository.UnitKerjaPatch{
		SetKode: request.Kode.Present,
		Kode:    request.Kode.Value,
		Nama:    request.Nama.Value,
		IsAktif: request.IsAktif.Value,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if !patch.SetKode && patch.Nama == nil && patch.IsAktif == nil {
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
		exists, err := c.UnitKerjaRepository.ExistsByKode(ctx, tx, *patch.Kode, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode unit kerja already used")
		}
	}

	unitKerja, err := c.UnitKerjaRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "kode unit kerja already used"), "unit kerja not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.UnitKerjaToResponse(unitKerja), nil
}

func (c *UnitKerjaUseCase) Search(ctx context.Context, request *model.ListUnitKerjaRequest) ([]model.UnitKerjaResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.UnitKerjaRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.UnitKerjaToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
