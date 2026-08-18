package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// SupplierController binds HTTP to the usecase — parse, call, write. No business
// logic and no SQL.
type SupplierController struct {
	Log     *logrus.Logger
	UseCase *usecase.SupplierUseCase
}

func NewSupplierController(log *logrus.Logger, useCase *usecase.SupplierUseCase) *SupplierController {
	return &SupplierController{Log: log, UseCase: useCase}
}

func (c *SupplierController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateSupplierRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.SupplierResponse]{Data: response})
}

func (c *SupplierController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetSupplierRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.SupplierResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID from the path, so a
// body that smuggles in an id cannot change which row is written.
func (c *SupplierController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateSupplierRequest)
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

	return ctx.JSON(model.WebResponse[*model.SupplierResponse]{Data: response})
}

func (c *SupplierController) List(ctx fiber.Ctx) error {
	request := new(model.ListSupplierRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.SupplierResponse]{Data: responses, Paging: paging})
}

// Utang lists this supplier's invoices that are still owed money, oldest first.
//
// The id is bound from the path after the query string, so an id smuggled into the query
// cannot point the answer at a different supplier than the URL names.
func (c *SupplierController) Utang(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ListUtangSupplierRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.IDSupplier = id

	responses, paging, err := c.UseCase.Utang(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.UtangSupplierResponse]{Data: responses, Paging: paging})
}
