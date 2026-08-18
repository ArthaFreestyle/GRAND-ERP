package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// RuangController binds HTTP to the usecase. It parses input, calls one
// usecase method, and writes the envelope — no business logic, no SQL.
type RuangController struct {
	Log     *logrus.Logger
	UseCase *usecase.RuangUseCase
}

func NewRuangController(log *logrus.Logger, useCase *usecase.RuangUseCase) *RuangController {
	return &RuangController{Log: log, UseCase: useCase}
}

func (c *RuangController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateRuangRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.RuangResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID and ActorID, so a body
// that smuggles in either cannot change which row is written or who is recorded.
func (c *RuangController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateRuangRequest)
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

	return ctx.JSON(model.WebResponse[*model.RuangResponse]{Data: response})
}

func (c *RuangController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetRuangRequest{
		ID: id, AktifIDUnitKerja: aktifIDUnitKerja(ctx),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.RuangResponse]{Data: response})
}

func (c *RuangController) List(ctx fiber.Ctx) error {
	request := new(model.ListRuangRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	// Bound first, then overwritten: a query string carrying its own value
	// cannot pick which unit scopes the list.
	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.RuangResponse]{Data: responses, Paging: paging})
}
