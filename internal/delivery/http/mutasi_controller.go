package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// MutasiController binds HTTP to the usecase — parse, call, write.
//
// Seven endpoints, not nine: there is no ajukan and no tolak, because mutasi has no
// DIAJUKAN state. The two-person control is still there, entirely in the route table —
// INVENTARIS reaches DRAFT, SUPERADMIN posts and voids. There is no DELETE either; a
// posted document is voided with reversing kartu_stok rows.
type MutasiController struct {
	Log     *logrus.Logger
	UseCase *usecase.MutasiUseCase
}

func NewMutasiController(log *logrus.Logger, useCase *usecase.MutasiUseCase) *MutasiController {
	return &MutasiController{Log: log, UseCase: useCase}
}

func (c *MutasiController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateMutasiRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	// Bound first, then overwritten: a body carrying its own actor cannot pick one.
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ActorID = actor
	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	response, err := c.UseCase.Create(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}

func (c *MutasiController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetMutasiRequest{
		ID: id, AktifIDUnitKerja: aktifIDUnitKerja(ctx),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}

// List is also SUPERADMIN's work queue: status=DRAFT with terlama_dulu=true is the set
// of documents waiting to be posted, oldest first. Without a DIAJUKAN state there is no
// other signal that a draft is ready.
func (c *MutasiController) List(ctx fiber.Ctx) error {
	request := new(model.ListMutasiRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.MutasiResponse]{Data: responses, Paging: paging})
}

func (c *MutasiController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateMutasiRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor
	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	response, err := c.UseCase.Update(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the lines are replaced wholesale.
func (c *MutasiController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplaceMutasiDetailRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.ReplaceDetail(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}

// Posting is the call that moves stock, twice per line. Guarded by SUPERADMIN in the
// route table — and with no DIAJUKAN state, that guard is the only control left.
func (c *MutasiController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingMutasiRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}

func (c *MutasiController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalMutasiRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.Batal(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.MutasiResponse]{Data: response})
}
