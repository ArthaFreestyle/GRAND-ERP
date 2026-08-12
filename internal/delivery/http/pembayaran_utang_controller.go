package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PembayaranUtangController binds HTTP to the usecase — parse, call, write.
//
// There is no DELETE and no ajukan: a posted payment is voided, and this document has no
// approval stage because nothing it writes is append-only. See
// entity.StatusPembayaranUtangDraft for why that is a decision rather than an omission.
type PembayaranUtangController struct {
	Log     *logrus.Logger
	UseCase *usecase.PembayaranUtangUseCase
}

func NewPembayaranUtangController(log *logrus.Logger, useCase *usecase.PembayaranUtangUseCase) *PembayaranUtangController {
	return &PembayaranUtangController{Log: log, UseCase: useCase}
}

func (c *PembayaranUtangController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePembayaranUtangRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

func (c *PembayaranUtangController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPembayaranUtangRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

func (c *PembayaranUtangController) List(ctx fiber.Ctx) error {
	request := new(model.ListPembayaranUtangRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PembayaranUtangResponse]{Data: responses, Paging: paging})
}

func (c *PembayaranUtangController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePembayaranUtangRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

// ReplaceAlokasi answers PUT, not PATCH: the allocations are replaced wholesale.
func (c *PembayaranUtangController) ReplaceAlokasi(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplacePembayaranUtangAlokasiRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.ReplaceAlokasi(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

// Posting is the call that settles the invoices. Guarded by SUPERADMIN in the route table.
func (c *PembayaranUtangController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingPembayaranUtangRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

func (c *PembayaranUtangController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalPembayaranUtangRequest)
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

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

// Cairkan records that a giro cleared the bank — the moment the payable actually drops.
func (c *PembayaranUtangController) Cairkan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.CairkanGiroRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.CairkanGiro(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}

// TolakGiro records that a giro bounced. Nothing is given back, because it never reduced a
// payable in the first place.
func (c *PembayaranUtangController) TolakGiro(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TolakGiroRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.TolakGiro(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PembayaranUtangResponse]{Data: response})
}
