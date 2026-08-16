package http

import (
	"strconv"

	"Arthafreestyle/ERP/internal/delivery/http/middleware"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// ProductController binds HTTP to the usecase — parse, call, write.
//
// This is the first controller that supplies an actor. product.created_by is NOT NULL,
// and the id comes from the verified token via middleware.SessionFrom — never from the
// request body, which anyone could set to anyone.
type ProductController struct {
	Log     *logrus.Logger
	UseCase *usecase.ProductUseCase
}

func NewProductController(log *logrus.Logger, useCase *usecase.ProductUseCase) *ProductController {
	return &ProductController{Log: log, UseCase: useCase}
}

// actorID pulls the caller's id off the session. The routes are behind the auth
// middleware, so a missing session means a wiring mistake rather than an anonymous
// caller — 401 is still the honest answer, since nothing proves who is asking.
func actorID(ctx fiber.Ctx) (int64, error) {
	session, ok := middleware.SessionFrom(ctx)
	if !ok {
		return 0, model.Unauthorized("authentication required")
	}

	return session.UserID, nil
}

// aktifIDUnitKerja returns the caller's active unit_kerja, or nil when the
// active grant applies everywhere (or, defensively, when there is no active
// grant at all — unreachable through any route guarded by RequireRole, since
// HasRole always answers false without one, but not assumed here). isu #12
// fase 5: this is what lets a usecase validate a write's id_ruang against the
// caller's actual authority instead of trusting it as sent.
func aktifIDUnitKerja(ctx fiber.Ctx) *int64 {
	session, ok := middleware.SessionFrom(ctx)
	if !ok || session.Aktif == nil {
		return nil
	}

	return session.Aktif.IDUnitKerja
}

func (c *ProductController) Create(ctx fiber.Ctx) error {
	request := new(model.CreateProductRequest)
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

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

func (c *ProductController) Get(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	response, err := c.UseCase.Get(ctx.Context(), &model.GetProductRequest{ID: id})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

// Update binds the body first and only then overwrites ID and ActorID, so a body that
// smuggles in either cannot change which row is written or who is recorded.
func (c *ProductController) Update(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.UpdateProductRequest)
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

	return ctx.JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

func (c *ProductController) AddSatuan(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.AddProductSatuanRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.IDProduct = id
	request.ActorID = actor

	response, err := c.UseCase.AddSatuan(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

func (c *ProductController) AddHargaJual(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.AddProductHargaJualRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.IDProduct = id
	request.ActorID = actor

	response, err := c.UseCase.AddHargaJual(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

// HargaBerlaku reports which price version applies, per satuan, on one date.
//
// The id is bound from the path after the query string, so an id_product smuggled
// into the query cannot point the answer at a different product than the URL names.
func (c *ProductController) HargaBerlaku(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ListHargaJualBerlakuRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.IDProduct = id

	responses, err := c.UseCase.HargaBerlaku(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.HargaJualBerlakuResponse]{Data: responses})
}

// UpdateHargaJual corrects the price on an existing version.
func (c *ProductController) UpdateHargaJual(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	idHarga, err := strconv.ParseInt(ctx.Params("id_harga"), 10, 64)
	if err != nil {
		return model.Invalid("id_harga must be an integer")
	}

	request := new(model.UpdateProductHargaJualRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	request.ID = idHarga
	request.IDProduct = id

	response, err := c.UseCase.UpdateHargaJual(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

// DeleteHargaJual removes a price version outright and reopens whatever version came
// before it, so no date range is left with no price at all.
func (c *ProductController) DeleteHargaJual(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	idHarga, err := strconv.ParseInt(ctx.Params("id_harga"), 10, 64)
	if err != nil {
		return model.Invalid("id_harga must be an integer")
	}

	response, err := c.UseCase.DeleteHargaJual(ctx.Context(), &model.DeleteProductHargaJualRequest{
		ID: idHarga, IDProduct: id,
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.ProductResponse]{Data: response})
}

// DaftarHargaJual lists the price in force across every product, one row per product
// and satuan.
func (c *ProductController) DaftarHargaJual(ctx fiber.Ctx) error {
	request := new(model.ListDaftarHargaJualRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.DaftarHargaJual(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.DaftarHargaJualResponse]{Data: responses, Paging: paging})
}

// RiwayatBeli reports the last posted purchase of this product from each supplier.
//
// The id is bound from the path after the query string, so an id_product smuggled into
// the query cannot point the answer at a different product than the URL names.
func (c *ProductController) RiwayatBeli(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.ListRiwayatBeliRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	request.IDProduct = id

	responses, paging, err := c.UseCase.RiwayatBeli(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.RiwayatBeliResponse]{Data: responses, Paging: paging})
}

// Stok reports where this product's stock currently sits, one row per room.
//
// No query parameters and no paging: the answer is one row per room the product has
// moved through, and every caller wants all of them to choose from.
func (c *ProductController) Stok(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	responses, err := c.UseCase.Stok(ctx.Context(), &model.ListStokProductRequest{
		IDProduct: id, AktifIDUnitKerja: aktifIDUnitKerja(ctx),
	})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.StokRuangResponse]{Data: responses})
}

// POS answers the POS catalog screen — isu #11. id_ruang is required and validated
// against ruang before anything else runs; an unknown one answers 404 rather than a
// page of silent zeros.
func (c *ProductController) POS(ctx fiber.Ctx) error {
	request := new(model.ListPosProductRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.POS(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.PosProductResponse]{Data: responses, Paging: paging})
}

func (c *ProductController) List(ctx fiber.Ctx) error {
	request := new(model.ListProductRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.ProductResponse]{Data: responses, Paging: paging})
}
