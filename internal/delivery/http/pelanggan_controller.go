package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PelangganController binds HTTP to the usecase — parse, call, write. No business
// logic and no SQL.
type PelangganController struct {
	Log     *logrus.Logger
	UseCase *usecase.PelangganUseCase
}

func NewPelangganController(log *logrus.Logger, useCase *usecase.PelangganUseCase) *PelangganController {
	return &PelangganController{Log: log, UseCase: useCase}
}

func (c *PelangganController) Create(ctx fiber.Ctx) error {
	request := new(model.CreatePelangganRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	response, err := c.UseCase.Create(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PelangganResponse]{Data: response})
}

func (c *PelangganController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPelangganRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PelangganResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID from the path, so a
// body that smuggles in an id cannot change which row is written.
func (c *PelangganController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePelangganRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	request.ID = id

	response, err := c.UseCase.Update(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PelangganResponse]{Data: response})
}

func (c *PelangganController) List(ctx fiber.Ctx) error {
	request := new(model.ListPelangganRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PelangganResponse]{Data: responses, Paging: paging})
}

// Piutang lists this customer's KREDIT notas that are still owed money, oldest
// first — isu #10 fase 2, the receivable-side mirror of SupplierController.Utang.
//
// The id is bound from the path after the query string, so an id smuggled into the
// query cannot point the answer at a different customer than the URL names.
func (c *PelangganController) Piutang(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ListPiutangPelangganRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.IDPelanggan = id

	responses, paging, err := c.UseCase.Piutang(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PiutangPelangganResponse]{Data: responses, Paging: paging})
}
