package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// PresensiController binds HTTP to the usecase — parse, call, write.
//
// The tap endpoints are the only writes in this API that bind no body at all.
// Everything they record comes from the verified session or the server's
// clock: binding a body here would create the one field that must never be
// bindable, id_user, and with it the ability to mark a colleague present who
// has not arrived.
type PresensiController struct {
	Log     *logrus.Logger
	UseCase *usecase.PresensiUseCase
}

func NewPresensiController(log *logrus.Logger, useCase *usecase.PresensiUseCase) *PresensiController {
	return &PresensiController{Log: log, UseCase: useCase}
}

// Masuk is the clock-in button. No body, no parameters, no choices.
func (c *PresensiController) Masuk(ctx fiber.Ctx) error {
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Masuk(ctx.Context(), &model.HadirRequest{
		IDUser:           actor,
		AktifIDUnitKerja: aktifIDUnitKerja(ctx),
		// ctx.IP() as-is, with no trusted-proxy configuration anywhere in this
		// project — the same caveat login throttling already carries.
		// Recorded so it is useful the day there is one, not because it
		// proves anything today.
		IPMasuk: ctx.IP(),
	})
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.PresensiResponse]{Data: response})
}

// Pulang is the clock-out button. Also no body: it closes whichever shift this
// caller currently has open, wherever that is.
func (c *PresensiController) Pulang(ctx fiber.Ctx) error {
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Pulang(ctx.Context(), &model.PulangRequest{
		IDUser:   actor,
		IPPulang: ctx.IP(),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PresensiResponse]{Data: response})
}

// HariIni reports both of today's shifts for the caller.
func (c *PresensiController) HariIni(ctx fiber.Ctx) error {
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.HariIni(ctx.Context(), &model.GetPresensiHariIniRequest{IDUser: actor})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.PresensiHariIniResponse]{Data: response})
}

// Saya reports the caller's own history, paginated.
//
// id_user and AktifIDUnitKerja are overwritten unconditionally after binding:
// your own history is always your own and is never scoped by unit — including
// the days you were working at another one.
func (c *PresensiController) Saya(ctx fiber.Ctx) error {
	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request := new(model.ListPresensiRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.IDUser = &actor
	request.AktifIDUnitKerja = nil

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PresensiResponse]{Data: responses, Paging: paging})
}

// List pages over every employee's attendance — SUPERADMIN only, scoped to the
// caller's active unit.
func (c *PresensiController) List(ctx fiber.Ctx) error {
	request := new(model.ListPresensiRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PresensiResponse]{Data: responses, Paging: paging})
}

func (c *PresensiController) Rekap(ctx fiber.Ctx) error {
	request := new(model.ListRekapPresensiRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Rekap(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.RekapPresensiResponse]{Data: responses, Paging: paging})
}

// Update corrects a recorded hour or shift, leaving a trail — SUPERADMIN only.
func (c *PresensiController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdatePresensiRequest)
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

	return ctx.JSON(model.WebResponse[*model.PresensiResponse]{Data: response})
}
