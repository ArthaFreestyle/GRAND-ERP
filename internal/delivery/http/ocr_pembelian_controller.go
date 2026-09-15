package http

import (
	"io"
	"strconv"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// fieldOCRFile is the multipart field both OCR endpoints read the photo from.
const fieldOCRFile = "file"

// OCRPembelianController binds HTTP to OCRPembelianUseCase — isu #39.
//
// It answers 200, never 201: nothing is created by either call. Both handlers share
// one shape (parse the multipart form, call the usecase, return the same envelope)
// and differ only in which usecase method they call — the two endpoints exist
// because the centang rule differs between them, not because the plumbing does.
type OCRPembelianController struct {
	Log     *logrus.Logger
	UseCase *usecase.OCRPembelianUseCase
}

func NewOCRPembelianController(log *logrus.Logger, useCase *usecase.OCRPembelianUseCase) *OCRPembelianController {
	return &OCRPembelianController{Log: log, UseCase: useCase}
}

func (c *OCRPembelianController) FakturKedatangan(ctx fiber.Ctx) error {
	request, gambar, err := c.bindRequest(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = gambar.Close()
	}()

	response, err := c.UseCase.FakturKedatangan(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.OCRPembelianResponse]{Data: response})
}

func (c *OCRPembelianController) Nota(ctx fiber.Ctx) error {
	request, gambar, err := c.bindRequest(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = gambar.Close()
	}()

	response, err := c.UseCase.Nota(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.OCRPembelianResponse]{Data: response})
}

// bindRequest reads the multipart form both endpoints share: the photo plus
// id_supplier, id_ruang, and an optional tanggal — the same three fields
// CreatePembelianRequest itself needs before it can name a room or a supplier.
//
// The returned io.ReadCloser is the caller's to close, after the usecase has
// finished reading it — closing it here, before the usecase runs, would hand it an
// already-closed reader.
func (c *OCRPembelianController) bindRequest(ctx fiber.Ctx) (*model.OCRPembelianRequest, io.ReadCloser, error) {
	header, err := ctx.FormFile(fieldOCRFile)
	if err != nil {
		return nil, nil, model.Invalid("file is required as multipart field '" + fieldOCRFile + "'")
	}

	gambar, err := header.Open()
	if err != nil {
		return nil, nil, model.Invalid("file tidak bisa dibaca")
	}

	actor, err := actorID(ctx)
	if err != nil {
		_ = gambar.Close()

		return nil, nil, err
	}

	idSupplier, err := strconv.ParseInt(ctx.FormValue("id_supplier"), 10, 64)
	if err != nil {
		_ = gambar.Close()

		return nil, nil, model.Invalid("id_supplier is required and must be an integer")
	}

	idRuang, err := strconv.ParseInt(ctx.FormValue("id_ruang"), 10, 64)
	if err != nil {
		_ = gambar.Close()

		return nil, nil, model.Invalid("id_ruang is required and must be an integer")
	}

	request := &model.OCRPembelianRequest{
		ActorID:          actor,
		AktifIDUnitKerja: aktifIDUnitKerja(ctx),
		Gambar:           gambar,
		UkuranDilaporkan: header.Size,
		IDSupplier:       idSupplier,
		IDRuang:          idRuang,
		Tanggal:          ctx.FormValue("tanggal"),
	}

	return request, gambar, nil
}
