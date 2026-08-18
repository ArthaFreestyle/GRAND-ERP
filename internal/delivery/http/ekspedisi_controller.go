package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// EkspedisiController binds HTTP to the usecase — parse, call, write. No
// business logic and no SQL.
type EkspedisiController struct {
	Log     *logrus.Logger
	UseCase *usecase.EkspedisiUseCase
}

func NewEkspedisiController(log *logrus.Logger, useCase *usecase.EkspedisiUseCase) *EkspedisiController {
	return &EkspedisiController{Log: log, UseCase: useCase}
}

func (c *EkspedisiController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateEkspedisiRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.EkspedisiResponse]{Data: response})
}

func (c *EkspedisiController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetEkspedisiRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.EkspedisiResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID from the path, so a
// body that smuggles in an id cannot change which row is written.
func (c *EkspedisiController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateEkspedisiRequest)
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

	return ctx.JSON(model.WebResponse[*model.EkspedisiResponse]{Data: response})
}

func (c *EkspedisiController) List(ctx fiber.Ctx) error {
	request := new(model.ListEkspedisiRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.EkspedisiResponse]{Data: responses, Paging: paging})
}
