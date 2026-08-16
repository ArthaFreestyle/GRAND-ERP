package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// StokOpnameController binds HTTP to the usecase — parse, call, write.
//
// PATCH .../detail/{id_detail} is the one deliberate exception to "lines are
// replaced wholesale" in this API — see model.UpdateStokOpnameDetailRequest.
// There is no DELETE; a posted document is voided with reversing kartu_stok
// rows, same as every other stock-writing module.
type StokOpnameController struct {
	Log     *logrus.Logger
	UseCase *usecase.StokOpnameUseCase
}

func NewStokOpnameController(log *logrus.Logger, useCase *usecase.StokOpnameUseCase) *StokOpnameController {
	return &StokOpnameController{Log: log, UseCase: useCase}
}

func (c *StokOpnameController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateStokOpnameRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

func (c *StokOpnameController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetStokOpnameRequest{
		ID: id, AktifIDUnitKerja: aktifIDUnitKerja(ctx),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// List is also the verification queue: status=DIAJUKAN with terlama_dulu=true is
// the set of counts waiting to be reviewed, oldest first — and since the freeze
// exists, it doubles as the list of rooms currently unable to post anything.
func (c *StokOpnameController) List(ctx fiber.Ctx) error {
	request := new(model.ListStokOpnameRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.AktifIDUnitKerja = aktifIDUnitKerja(ctx)

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.StokOpnameResponse]{Data: responses, Paging: paging})
}

func (c *StokOpnameController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateStokOpnameRequest)
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

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// TarikSaldo takes no body: it seeds every line from the room's own balance.
func (c *StokOpnameController) TarikSaldo(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.TarikSaldo(ctx.Context(), &model.TarikSaldoStokOpnameRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// ReplaceDetail answers PUT, not PATCH: the line set is replaced wholesale. This
// is also how a line TarikSaldo's automatic pull missed gets added by hand.
func (c *StokOpnameController) ReplaceDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ReplaceStokOpnameDetailRequest)
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

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// UpdateDetail is the deliberate exception to "lines are replaced wholesale" —
// see model.UpdateStokOpnameDetailRequest for why a count sheet needs it.
func (c *StokOpnameController) UpdateDetail(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	idDetail, err := strconv.ParseInt(ctx.Params("id_detail"), 10, 64)
	if err != nil {
		return model.Invalid("id_detail must be an integer")
	}

	request := new(model.UpdateStokOpnameDetailRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ID = id
	request.IDDetail = idDetail
	request.ActorID = actor

	response, err := c.UseCase.UpdateDetail(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// Ajukan takes no body: everything the transition needs is on the document already.
func (c *StokOpnameController) Ajukan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Ajukan(ctx.Context(), &model.AjukanStokOpnameRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// Tolak takes no body either — the schema has no column to hold a reason for
// this document's rejection. See StokOpnameRepository.Tolak.
func (c *StokOpnameController) Tolak(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Tolak(ctx.Context(), &model.TolakStokOpnameRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// Posting is the call that writes the selisih. Guarded by SUPERADMIN in the
// route table, and it is what releases the freeze.
func (c *StokOpnameController) Posting(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Posting(ctx.Context(), &model.PostingStokOpnameRequest{
		ID: id, ActorID: actor,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}

// Batal may be called from any non-BATAL status — see StokOpnameUseCase.Batal —
// and it is what releases the freeze when a count is abandoned before posting.
func (c *StokOpnameController) Batal(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.BatalStokOpnameRequest)
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

	return ctx.JSON(model.WebResponse[*model.StokOpnameResponse]{Data: response})
}
