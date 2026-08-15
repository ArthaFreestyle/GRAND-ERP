package usecase

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// RuangUseCase holds the business rules for ruang. It owns the transaction
// boundary and returns models, never entities or Fiber types.
type RuangUseCase struct {
	DB              *sql.DB
	Log             *logrus.Logger
	Validate        *validator.Validate
	RuangRepository *repository.RuangRepository
	// UnitKerjaRepository is borrowed to validate id_unit_kerja is an active
	// unit before Create writes — isu #12 fase 2. The same reasoning
	// RoleRepository.CountActiveByIDs applies to role grants: the foreign key
	// alone cannot tell a retired unit from a live one.
	UnitKerjaRepository *repository.UnitKerjaRepository
}

func NewRuangUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	ruangRepository *repository.RuangRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
) *RuangUseCase {
	return &RuangUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		RuangRepository:     ruangRepository,
		UnitKerjaRepository: unitKerjaRepository,
	}
}

func (c *RuangUseCase) Create(ctx context.Context, request *model.CreateRuangRequest) (*model.RuangResponse, error) {
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

	// Kode is optional; uniqueness only matters when one is supplied.
	if request.Kode != nil {
		exists, err := c.RuangRepository.ExistsByKode(ctx, tx, *request.Kode)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode ruang already used")
		}
	}

	// FindByID doubles as the active-unit check: the foreign key alone cannot
	// tell a retired unit from one that never existed, and its message names a
	// constraint rather than the field — checked here for the same reason role
	// ids are checked before granting. It also supplies the unit's name, which
	// the response needs and Create's own RETURNING (id only) does not provide.
	unit, err := c.UnitKerjaRepository.FindByID(ctx, tx, request.IDUnitKerja)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err != nil || !unit.IsAktif {
		return nil, model.Invalid("id_unit_kerja is not a known active unit kerja")
	}

	ruang := &entity.Ruang{
		Kode:          request.Kode,
		NamaRuang:     request.NamaRuang,
		IsAktif:       true,
		IDUnitKerja:   request.IDUnitKerja,
		NamaUnitKerja: unit.Nama,
	}

	if err := c.RuangRepository.Create(ctx, tx, ruang); err != nil {
		return nil, invalidOnForeignKey(err, "id_unit_kerja is not a known active unit kerja")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.RuangToResponse(ruang), nil
}

func (c *RuangUseCase) Get(ctx context.Context, request *model.GetRuangRequest) (*model.RuangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	ruang, err := c.RuangRepository.FindByID(ctx, c.DB, request.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.NotFound("ruang not found")
		}

		return nil, err
	}

	// isu #12 fase 6: a room outside the caller's active unit answers 404, the
	// same as one that does not exist — a scoped read must not confirm the
	// room is there.
	if diLuarUnitAktif(ruang.IDUnitKerja, request.AktifIDUnitKerja) {
		return nil, model.NotFound("ruang not found")
	}

	return converter.RuangToResponse(ruang), nil
}

func (c *RuangUseCase) Search(ctx context.Context, request *model.ListRuangRequest) ([]model.RuangResponse, *model.PageMetadata, error) {
	request.Normalize()

	if err := c.Validate.Struct(request); err != nil {
		return nil, nil, err
	}

	list, total, err := c.RuangRepository.Search(
		ctx, c.DB, request.Search, request.IsAktif, request.AktifIDUnitKerja,
		request.Size, request.Offset(),
	)
	if err != nil {
		return nil, nil, err
	}

	paging := &model.PageMetadata{
		Page:      request.Page,
		Size:      request.Size,
		TotalItem: total,
		TotalPage: int64(math.Ceil(float64(total) / float64(request.Size))),
	}

	return converter.RuangToResponses(list), paging, nil
}
