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

// RoleUseCase holds the business rules for role. It owns the transaction boundary
// and returns models, never entities or Fiber types.
type RoleUseCase struct {
	DB             *sql.DB
	Log            *logrus.Logger
	Validate       *validator.Validate
	RoleRepository *repository.RoleRepository
}

func NewRoleUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	roleRepository *repository.RoleRepository,
) *RoleUseCase {
	return &RoleUseCase{
		DB:             db,
		Log:            log,
		Validate:       validate,
		RoleRepository: roleRepository,
	}
}

func (c *RoleUseCase) Create(ctx context.Context, request *model.CreateRoleRequest) (*model.RoleResponse, error) {
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

	exists, err := c.RoleRepository.ExistsByNama(ctx, tx, request.Nama, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, model.Conflict("nama role already used")
	}

	role := &entity.Role{
		Nama:      request.Nama,
		IsAktif:   true,
		CreatedBy: &request.ActorID,
	}

	if err := c.RoleRepository.Create(ctx, tx, role); err != nil {
		return nil, conflictOnUnique(err, "nama role already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.RoleToResponse(role), nil
}

func (c *RoleUseCase) Get(ctx context.Context, request *model.GetRoleRequest) (*model.RoleResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	role, err := c.RoleRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		return nil, notFoundOnNoRows(err, "role not found")
	}

	return converter.RoleToResponse(role), nil
}

func (c *RoleUseCase) Update(ctx context.Context, request *model.UpdateRoleRequest) (*model.RoleResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// Both columns are NOT NULL: changeable, never clearable.
	if request.Nama.Clears() {
		return nil, model.Invalid("nama cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.RolePatch{
		Nama:         request.Nama.Value,
		IsAktif:      request.IsAktif.Value,
		SetUpdatedBy: true,
		UpdatedBy:    &request.ActorID,
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
		exists, err := c.RoleRepository.ExistsByNama(ctx, tx, *patch.Nama, request.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("nama role already used")
		}
	}

	role, err := c.RoleRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "nama role already used"), "role not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.RoleToResponse(role), nil
}

func (c *RoleUseCase) Search(ctx context.Context, request *model.ListRoleRequest) ([]model.RoleResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.RoleRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	return converter.RoleToResponses(list), pageMetadata(&request.PageRequest, total), nil
}
