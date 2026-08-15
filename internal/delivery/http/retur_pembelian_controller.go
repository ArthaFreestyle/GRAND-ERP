package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// ReturPembelianController binds HTTP to the usecase — parse, call, write.
//
// Same shape as PenerimaanSusulanController, and behind the same split: the desk that
// packs the goods being sent back is not the desk that lets them out of the stock
// ledger. There is no DELETE; a posted document is voided with reversing kartu_stok
// rows.
type ReturPembelianController struct {
	Log     *logrus.Logger
	UseCase *usecase.ReturPembelianUseCase
}

func NewReturPembelianController(log *logrus.Logger, useCase *usecase.ReturPembelianUseCase) *ReturPembelianController {
	return &ReturPembelianController{Log: log, UseCase: useCase}
}

func (c *ReturPembelianController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateReturPembelianRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	// Bound first, then overwritten: a body carrying its own actor cannot pick one.
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ActorID = actor

	response, err := c.UseCase.Create(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

func (c *ReturPembelianController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetReturPembelianRequest{
		ID: id, AktifIDUnitKerja: aktifIDUnitKerja(ctx),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

func (c *ReturPembelianController) List(ctx fiber.Ctx) error {
	request := new(model.ListReturPembelianRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.ReturPembelianResponse]{Data: responses, Paging: paging})
}

func (c *ReturPembelianController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateReturPembelianRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.Update(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the lines are replaced wholesale.
func (c *ReturPembelianController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplaceReturPembelianDetailRequest)
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

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

// Ajukan takes no body: everything the transition needs is on the document already.
func (c *ReturPembelianController) Ajukan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Ajukan(ctx.Context(), &model.AjukanReturPembelianRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

func (c *ReturPembelianController) Tolak(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TolakReturPembelianRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.Tolak(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

// Posting is the call that moves stock. Guarded by SUPERADMIN in the route table.
func (c *ReturPembelianController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingReturPembelianRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}

func (c *ReturPembelianController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalReturPembelianRequest)
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

	return ctx.JSON(model.WebResponse[*model.ReturPembelianResponse]{Data: response})
}
