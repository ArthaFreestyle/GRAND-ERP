package http

import (
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// LaporanController binds HTTP to LaporanUseCase — isu #22 fase 3's three reports.
// None of them takes a body or writes anything; every handler here is bind query,
// call, write.
type LaporanController struct {
	Log     *logrus.Logger
	UseCase *usecase.LaporanUseCase
}

func NewLaporanController(log *logrus.Logger, useCase *usecase.LaporanUseCase) *LaporanController {
	return &LaporanController{Log: log, UseCase: useCase}
}

// NilaiPersediaan reports current inventory value, one row per room.
func (c *LaporanController) NilaiPersediaan(ctx fiber.Ctx) error {
	request := new(model.ListNilaiPersediaanRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, err := c.UseCase.NilaiPersediaan(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.NilaiPersediaanResponse]{Data: responses})
}

// LabaKotor reports gross margin grouped by month.
func (c *LaporanController) LabaKotor(ctx fiber.Ctx) error {
	request := new(model.ListLabaKotorRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, err := c.UseCase.LabaKotor(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.LabaKotorResponse]{Data: responses})
}

// Pergerakan reports a movement recap grouped by product, room, and jenis_transaksi.
func (c *LaporanController) Pergerakan(ctx fiber.Ctx) error {
	request := new(model.ListPergerakanRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, err := c.UseCase.Pergerakan(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PergerakanResponse]{Data: responses})
}
