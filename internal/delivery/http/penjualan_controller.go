package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PenjualanController binds HTTP to the usecase — parse, call, write.
//
// Seven endpoints, the same shape as mutasi's: no ajukan and no tolak, because
// penjualan has no DIAJUKAN state either — see PenjualanUseCase. There is no
// DELETE; a posted nota is voided with reversing kartu_stok rows.
type PenjualanController struct {
	Log     *logrus.Logger
	UseCase *usecase.PenjualanUseCase
}

func NewPenjualanController(log *logrus.Logger, useCase *usecase.PenjualanUseCase) *PenjualanController {
	return &PenjualanController{Log: log, UseCase: useCase}
}

func (c *PenjualanController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePenjualanRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}

func (c *PenjualanController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPenjualanRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}

func (c *PenjualanController) List(ctx fiber.Ctx) error {
	request := new(model.ListPenjualanRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PenjualanResponse]{Data: responses, Paging: paging})
}

func (c *PenjualanController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePenjualanRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the lines are replaced wholesale.
func (c *PenjualanController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplacePenjualanDetailRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}

// Posting is the call that moves stock and, for a KREDIT nota, is where
// plafon_kredit is enforced. Guarded by CASHIER in the route table — the same actor
// who created the nota may post it, which is the whole point of dropping DIAJUKAN.
func (c *PenjualanController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingPenjualanRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}

// Batal is the only control left once a nota is posted — guarded by SUPERADMIN in
// the route table, never the cashier who posted it.
func (c *PenjualanController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalPenjualanRequest)
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

	return ctx.JSON(model.WebResponse[*model.PenjualanResponse]{Data: response})
}
