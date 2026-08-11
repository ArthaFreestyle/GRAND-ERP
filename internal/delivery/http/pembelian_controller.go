package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PembelianController binds HTTP to the usecase — parse, call, write.
//
// Every handler here supplies an actor from the verified token, never from the
// body: pembelian.created_by is NOT NULL, and who submitted, approved, or voided a
// document is the audit trail the approval flow exists to produce.
//
// There is no DELETE. A posted document is voided with reversing kartu_stok rows,
// not removed.
type PembelianController struct {
	Log     *logrus.Logger
	UseCase *usecase.PembelianUseCase
}

func NewPembelianController(log *logrus.Logger, useCase *usecase.PembelianUseCase) *PembelianController {
	return &PembelianController{Log: log, UseCase: useCase}
}

func (c *PembelianController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePembelianRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPembelianRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) List(ctx fiber.Ctx) error {
	request := new(model.ListPembelianRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PembelianResponse]{Data: responses, Paging: paging})
}

// Update binds the body first and only then overwrites ID and ActorID, so a body
// that smuggles in either cannot change which row is written or who is recorded.
func (c *PembelianController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePembelianRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the lines are replaced wholesale.
func (c *PembelianController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplacePembelianDetailRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

// Ajukan takes no body: everything the transition needs is on the document already.
func (c *PembelianController) Ajukan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Ajukan(ctx.Context(), &model.AjukanPembelianRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) Tolak(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TolakPembelianRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

// Posting is the call that moves stock. Guarded by SUPERADMIN in the route table.
func (c *PembelianController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingPembelianRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalPembelianRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) BagiRataKoli(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.BagiRataKoli(ctx.Context(), &model.BagiRataKoliRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembelianResponse]{Data: response})
}

func (c *PembelianController) Sisa(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Sisa(ctx.Context(), &model.GetPembelianRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.SisaPembelianResponse]{Data: response})
}
