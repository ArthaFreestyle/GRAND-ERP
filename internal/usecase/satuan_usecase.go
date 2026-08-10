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

// SatuanUseCase holds the business rules for satuan. It owns the transaction
// boundary and returns models, never entities or Fiber types.
type SatuanUseCase struct {
	DB               *sql.DB
	Log              *logrus.Logger
	Validate         *validator.Validate
	SatuanRepository *repository.SatuanRepository
}

func NewSatuanUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	satuanRepository *repository.SatuanRepository,
) *SatuanUseCase {
	return &SatuanUseCase{
		DB:               db,
		Log:              log,
		Validate:         validate,
		SatuanRepository: satuanRepository,
	}
}

func (c *SatuanUseCase) Create(ctx context.Context, request *model.CreateSatuanRequest) (*model.SatuanResponse, error) {
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

	exists, err := c.SatuanRepository.ExistsByNama(ctx, tx, request.Nama, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, model.Conflict("nama satuan already used")
	}

	satuan := &entity.Satuan{
		Nama:    request.Nama,
		IsAktif: true,
	}

	if err := c.SatuanRepository.Create(ctx, tx, satuan); err != nil {
		return nil, conflictOnUnique(err, "nama satuan already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.SatuanToResponse(satuan), nil
}

func (c *SatuanUseCase) Get(ctx context.Context, request *model.GetSatuanRequest) (*model.SatuanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	satuan, err := c.SatuanRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "satuan not found")
	}

	return converter.SatuanToResponse(satuan), nil
}

func (c *SatuanUseCase) Update(ctx context.Context, request *model.UpdateSatuanRequest) (*model.SatuanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// nama and is_aktif are NOT NULL: they can be changed but never cleared.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.SatuanPatch{
		Nama:    request.Nama.Value,
		IsAktif: request.IsAktif.Value,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if patch.Nama == nil && patch.IsAktif == nil {
		return nil, model.Invalid("no fields to update")
	}

	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if patch.Nama != nil {
		exists, err := c.SatuanRepository.ExistsByNama(ctx, tx, *patch.Nama, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("nama satuan already used")
		}
	}

	satuan, err := c.SatuanRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "nama satuan already used"), "satuan not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.SatuanToResponse(satuan), nil
}

func (c *SatuanUseCase) Search(ctx context.Context, request *model.ListSatuanRequest) ([]model.SatuanResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.SatuanRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.SatuanToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
