package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// SatuanController binds HTTP to the usecase. It parses input, calls one
// usecase method, and writes the envelope — no business logic, no SQL.
type SatuanController struct {
	Log     *logrus.Logger
	UseCase *usecase.SatuanUseCase
}

func NewSatuanController(log *logrus.Logger, useCase *usecase.SatuanUseCase) *SatuanController {
	return &SatuanController{Log: log, UseCase: useCase}
}

func (c *SatuanController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateSatuanRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ActorID = actor

	response, err := c.UseCase.Create(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.SatuanResponse]{Data: response})
}

func (c *SatuanController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetSatuanRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.SatuanResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID from the path, so a
// body that smuggles in an id cannot change which row is written.
func (c *SatuanController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateSatuanRequest)
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

	return ctx.JSON(model.WebResponse[*model.SatuanResponse]{Data: response})
}

func (c *SatuanController) List(ctx fiber.Ctx) error {
	request := new(model.ListSatuanRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.SatuanResponse]{Data: responses, Paging: paging})
}
