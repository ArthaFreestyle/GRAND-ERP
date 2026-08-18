package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	// KartuStokRepository is borrowed for exactly one read — isu #23 fase 3:
	// whether a room being retired still holds stock. The question is over
	// kartu_stok's own table, so it stays in that module's repository.
	KartuStokRepository *repository.KartuStokRepository
}

func NewRuangUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	ruangRepository *repository.RuangRepository,
	unitKerjaRepository *repository.UnitKerjaRepository,
	kartuStokRepository *repository.KartuStokRepository,
) *RuangUseCase {
	return &RuangUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		RuangRepository:     ruangRepository,
		UnitKerjaRepository: unitKerjaRepository,
		KartuStokRepository: kartuStokRepository,
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
		CreatedBy:     &request.ActorID,
	}

	if err := c.RuangRepository.Create(ctx, tx, ruang); err != nil {
		return nil, invalidOnForeignKey(err, "id_unit_kerja is not a known active unit kerja")
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return converter.RuangToResponse(ruang), nil
}

// Update patches kode, nama_ruang, and is_aktif — isu #23 fase 2. id_unit_kerja
// is not in the DTO at all; see UpdateRuangRequest for why moving a ruang
// between units is refused outright rather than validated.
func (c *RuangUseCase) Update(ctx context.Context, request *model.UpdateRuangRequest) (*model.RuangResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	// nama_ruang and is_aktif are NOT NULL: changeable, never clearable. kode
	// may legitimately be cleared to null.
	if request.NamaRuang.Clears() {
		return nil, model.Invalid("nama_ruang cannot be null")
	}
	if request.IsAktif.Clears() {
		return nil, model.Invalid("is_aktif cannot be null")
	}

	patch := repository.RuangPatch{
		SetKode:      request.Kode.Present,
		Kode:         request.Kode.Value,
		NamaRuang:    request.NamaRuang.Value,
		IsAktif:      request.IsAktif.Value,
		SetUpdatedBy: true,
		UpdatedBy:    &request.ActorID,
	}

	// An empty body would still fire the updated_at trigger, recording a change
	// that never happened.
	if !patch.SetKode && patch.NamaRuang == nil && patch.IsAktif == nil {
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
		exists, err := c.RuangRepository.ExistsByKode(ctx, tx, *patch.Kode)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, model.Conflict("kode ruang already used")
		}
	}

	// isu #23 fase 3: retiring a room is refused while it still holds stock, or
	// while it is frozen by an open stok_opname. Both checks apply only when
	// this patch is the one asking is_aktif to become false — a patch that
	// leaves is_aktif untouched, or one re-affirming an already-inactive room,
	// changes nothing about whether the room may hold goods, so there is
	// nothing to guard.
	if patch.IsAktif != nil && !*patch.IsAktif {
		hasSaldo, err := c.KartuStokRepository.HasSaldoPositif(ctx, tx, request.ID)
		if err != nil {
			return nil, err
		}
		if hasSaldo {
			return nil, model.Conflict(
				"ruang masih memegang barang; kosongkan dengan mutasi atau pemakaian sebelum dipensiunkan",
			)
		}

		// RuangResponse.NomorOpnameBeku already carries this answer on every
		// read, so the rejection can name the opname holding the room without a
		// second lookup of its own.
		current, err := c.RuangRepository.FindByID(ctx, tx, request.ID)
		if err != nil {
			return nil, notFoundOnNoRows(err, "ruang not found")
		}
		if current.NomorOpnameBeku != nil {
			return nil, model.Conflict(fmt.Sprintf(
				"ruang sedang dibekukan oleh stok opname %s", *current.NomorOpnameBeku,
			))
		}
	}

	ruang, err := c.RuangRepository.Update(ctx, tx, request.ID, patch)
	if err != nil {
		return nil, notFoundOnNoRows(
			conflictOnUnique(err, "kode ruang already used"), "ruang not found",
		)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Update's RETURNING carries no join: NamaUnitKerja and NomorOpnameBeku need
	// the one FindByID/Search use, and an UPDATE cannot join. Re-read once,
	// after commit, the same way ProductUseCase.Update calls c.detail.
	full, err := c.RuangRepository.FindByID(ctx, c.DB, ruang.ID)
	if err != nil {
		return nil, err
	}

	return converter.RuangToResponse(full), nil
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
