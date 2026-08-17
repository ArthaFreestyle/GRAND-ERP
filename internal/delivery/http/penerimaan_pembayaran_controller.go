package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PenerimaanPembayaranController binds HTTP to the usecase — parse, call, write.
//
// There is no DELETE and no ajukan: a posted payment is voided, and this document
// has no approval stage because nothing it writes is append-only — the mirror of
// PembayaranUtangController with the money flowing the other way. See
// entity.StatusPenerimaanPembayaranDraft.
type PenerimaanPembayaranController struct {
	Log     *logrus.Logger
	UseCase *usecase.PenerimaanPembayaranUseCase
}

func NewPenerimaanPembayaranController(log *logrus.Logger, useCase *usecase.PenerimaanPembayaranUseCase) *PenerimaanPembayaranController {
	return &PenerimaanPembayaranController{Log: log, UseCase: useCase}
}

func (c *PenerimaanPembayaranController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePenerimaanPembayaranRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

func (c *PenerimaanPembayaranController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPenerimaanPembayaranRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

func (c *PenerimaanPembayaranController) List(ctx fiber.Ctx) error {
	request := new(model.ListPenerimaanPembayaranRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PenerimaanPembayaranResponse]{Data: responses, Paging: paging})
}

func (c *PenerimaanPembayaranController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePenerimaanPembayaranRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

// ReplaceAlokasi answers PUT, not PATCH: the allocations are replaced wholesale.
func (c *PenerimaanPembayaranController) ReplaceAlokasi(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplacePenerimaanPembayaranAlokasiRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

// Posting is the call that settles the notas. Guarded by SUPERADMIN in the route
// table.
func (c *PenerimaanPembayaranController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingPenerimaanPembayaranRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

func (c *PenerimaanPembayaranController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalPenerimaanPembayaranRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

// Cairkan records that a customer's giro cleared the bank — the moment the
// receivable actually drops.
func (c *PenerimaanPembayaranController) Cairkan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.CairkanGiroPelangganRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}

// TolakGiro records that a customer's giro bounced. Nothing is given back, because
// it never reduced a receivable in the first place.
func (c *PenerimaanPembayaranController) TolakGiro(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TolakGiroPelangganRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenerimaanPembayaranResponse]{Data: response})
}
