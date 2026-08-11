package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PenerimaanSusulanController binds HTTP to the usecase — parse, call, write.
//
// Same shape as PembelianController, and behind the same split: the desk that counts
// the late shipment is not the desk that lets those numbers into the stock ledger.
// There is no DELETE; a posted document is voided with reversing kartu_stok rows.
type PenerimaanSusulanController struct {
	Log     *logrus.Logger
	UseCase *usecase.PenerimaanSusulanUseCase
}

func NewPenerimaanSusulanController(log *logrus.Logger, useCase *usecase.PenerimaanSusulanUseCase) *PenerimaanSusulanController {
	return &PenerimaanSusulanController{Log: log, UseCase: useCase}
}

func (c *PenerimaanSusulanController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePenerimaanSusulanRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

func (c *PenerimaanSusulanController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPenerimaanSusulanRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

func (c *PenerimaanSusulanController) List(ctx fiber.Ctx) error {
	request := new(model.ListPenerimaanSusulanRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PenerimaanSusulanResponse]{Data: responses, Paging: paging})
}

func (c *PenerimaanSusulanController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePenerimaanSusulanRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the lines are replaced wholesale.
func (c *PenerimaanSusulanController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplacePenerimaanSusulanDetailRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

// Ajukan takes no body: everything the transition needs is on the document already.
func (c *PenerimaanSusulanController) Ajukan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Ajukan(ctx.Context(), &model.AjukanPenerimaanSusulanRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

func (c *PenerimaanSusulanController) Tolak(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TolakPenerimaanSusulanRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

// Posting is the call that moves stock. Guarded by SUPERADMIN in the route table.
func (c *PenerimaanSusulanController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingPenerimaanSusulanRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}

func (c *PenerimaanSusulanController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalPenerimaanSusulanRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanSusulanResponse]{Data: response})
}
