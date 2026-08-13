package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PeriodeController binds HTTP to the usecase — parse, call, write.
//
// The routes are keyed on (tahun, bulan) rather than on an id, and the departure from
// the /{id} pattern everywhere else is deliberate: that pair is what identifies a
// period, as periode_tahun_bulan_uidx already declares. An id-keyed route could not
// even address the common case, since a month nobody has closed has no row and
// therefore no id.
//
// There is no DELETE and no PATCH. A month closed by mistake is reopened; nothing
// else about a period is editable, because there is nothing else in it that anyone
// chose.
type PeriodeController struct {
	Log     *logrus.Logger
	UseCase *usecase.PeriodeUseCase
}

func NewPeriodeController(log *logrus.Logger, useCase *usecase.PeriodeUseCase) *PeriodeController {
	return &PeriodeController{Log: log, UseCase: useCase}
}

func (c *PeriodeController) Get(ctx fiber.Ctx) error {
	tahun, bulan, err := tahunBulan(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetPeriodeRequest{Tahun: tahun, Bulan: bulan})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PeriodeResponse]{Data: response})
}

func (c *PeriodeController) List(ctx fiber.Ctx) error {
	request := new(model.ListPeriodeRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PeriodeResponse]{Data: responses, Paging: paging})
}

// Tutup closes a month. No body is read at all: everything being asked is in the
// path, and who is asking comes from the token.
func (c *PeriodeController) Tutup(ctx fiber.Ctx) error {
	tahun, bulan, err := tahunBulan(ctx)
	if err != nil {
		return err
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Tutup(ctx.Context(), &model.TutupPeriodeRequest{
		Tahun: tahun, Bulan: bulan, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PeriodeResponse]{Data: response})
}

func (c *PeriodeController) Buka(ctx fiber.Ctx) error {
	tahun, bulan, err := tahunBulan(ctx)
	if err != nil {
		return err
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Buka(ctx.Context(), &model.BukaPeriodeRequest{
		Tahun: tahun, Bulan: bulan, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PeriodeResponse]{Data: response})
}

// tahunBulan parses the two path parameters that stand in for this module's id.
//
// Range is left to the usecase's validator, which names the field; what fails here is
// only text that is not a number at all.
func tahunBulan(ctx fiber.Ctx) (int, int, error) {
	tahun, err := strconv.Atoi(ctx.Params("tahun"))
	if err != nil {
		return 0, 0, model.Invalid("tahun must be an integer")
	}

	bulan, err := strconv.Atoi(ctx.Params("bulan"))
	if err != nil {
		return 0, 0, model.Invalid("bulan must be an integer")
	}

	return tahun, bulan, nil
}
