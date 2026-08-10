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

// EkspedisiUseCase holds the business rules for ekspedisi. It owns the
// transaction boundary and returns models, never entities or Fiber types.
type EkspedisiUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	EkspedisiRepository *repository.EkspedisiRepository
}

func NewEkspedisiUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	ekspedisiRepository *repository.EkspedisiRepository,
) *EkspedisiUseCase {
	return &EkspedisiUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		EkspedisiRepository: ekspedisiRepository,
	}
}

func (c *EkspedisiUseCase) Create(ctx context.Context, request *model.CreateEkspedisiRequest) (*model.EkspedisiResponse, error) {
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

	exists, err := c.EkspedisiRepository.ExistsByNama(ctx, tx, request.Nama, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, model.Conflict("nama ekspedisi already used")
	}

	ekspedisi := &entity.Ekspedisi{
		Nama:    request.Nama,
		Telepon: request.Telepon,
		IsAktif: true,
	}

	if err := c.EkspedisiRepository.Create(ctx, tx, ekspedisi); err != nil {
		return nil, conflictOnUnique(err, "nama ekspedisi already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.EkspedisiToResponse(ekspedisi), nil
}

func (c *EkspedisiUseCase) Get(ctx context.Context, request *model.GetEkspedisiRequest) (*model.EkspedisiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	ekspedisi, err := c.EkspedisiRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "ekspedisi not found")
	}

	return converter.EkspedisiToResponse(ekspedisi), nil
}

func (c *EkspedisiUseCase) Update(ctx context.Context, request *model.UpdateEkspedisiRequest) (*model.EkspedisiResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// nama and is_aktif are NOT NULL: changeable, never clearable.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.EkspedisiPatch{
		Nama:       request.Nama.Value,
		SetTelepon: request.Telepon.Present,
		Telepon:    request.Telepon.Value,
		IsAktif:    request.IsAktif.Value,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if patch.Nama == nil && !patch.SetTelepon && patch.IsAktif == nil {
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
		exists, err := c.EkspedisiRepository.ExistsByNama(ctx, tx, *patch.Nama, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("nama ekspedisi already used")
		}
	}

	ekspedisi, err := c.EkspedisiRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "nama ekspedisi already used"), "ekspedisi not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.EkspedisiToResponse(ekspedisi), nil
}

func (c *EkspedisiUseCase) Search(ctx context.Context, request *model.ListEkspedisiRequest) ([]model.EkspedisiResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.EkspedisiRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.EkspedisiToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
