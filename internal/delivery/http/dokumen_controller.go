package http

import (
	"net/url"
	"strconv"
	"strings"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// fieldBerkas is the multipart field the upload endpoint reads.
const fieldBerkas = "berkas"

// DokumenController binds HTTP to the usecase — parse, call, write.
//
// It is the only controller handling a body that is not JSON, and the only one
// writing a response that is not the standard envelope: a file's bytes are the
// response for a download. Everything else here follows the usual shape.
type DokumenController struct {
	Log     *logrus.Logger
	UseCase *usecase.DokumenUseCase
}

func NewDokumenController(log *logrus.Logger, useCase *usecase.DokumenUseCase) *DokumenController {
	return &DokumenController{Log: log, UseCase: useCase}
}

// Upload accepts one file from a multipart form and stores it as an orphan.
//
// The handler opens the part and hands the usecase a plain io.Reader, which is what
// keeps "this arrived over HTTP" out of the business layer — and what lets the
// upload rules be tested with a bytes.Reader and no server at all.
func (c *DokumenController) Upload(ctx fiber.Ctx) error {
	header, err := ctx.FormFile(fieldBerkas)
	if err != nil {
		return model.Invalid("berkas is required as multipart field '" + fieldBerkas + "'")
	}

	berkas, err := header.Open()
	if err != nil {
		return model.Invalid("berkas tidak bisa dibaca")
	}
	defer func() {
		_ = berkas.Close()
	}()

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request := &model.UploadDokumenRequest{
		// The client's filename is carried as display text only. It is never joined
		// onto a path, so it is free to be "../../config.json" and mean nothing.
		NamaAsli:         header.Filename,
		Berkas:           berkas,
		UkuranDilaporkan: header.Size,
		ActorID:          actor,
	}

	response, err := c.UseCase.Upload(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusCreated).JSON(model.WebResponse[*model.DokumenResponse]{Data: response})
}

// Isi streams one attachment's bytes.
//
// Three headers matter here and none of them is decoration:
//
//   - Content-Disposition: attachment stops the browser rendering the file in the
//     application's own origin. A stored HTML or SVG rendered there is script running
//     with the user's session.
//   - X-Content-Type-Options: nosniff stops the browser second-guessing the type and
//     arriving at that same place by another road.
//   - Cache-Control: private, no-store keeps a photographed invoice — purchase prices
//     and a supplier's identity — out of shared caches and off disk.
//
// The stream is closed by fasthttp once the response is written; it closes a body
// stream that implements io.Closer, which the storage layer's reader does.
func (c *DokumenController) Isi(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	dokumen, berkas, err := c.UseCase.Isi(ctx.Context(), &model.GetDokumenRequest{ID: id, ActorID: actor})
	if err != nil {
		return err
	}

	ctx.Set(fiber.HeaderContentType, dokumen.Mime)
	ctx.Set(fiber.HeaderContentDisposition, dispositionLampiran(dokumen.NamaAsli))
	ctx.Set("X-Content-Type-Options", "nosniff")
	ctx.Set(fiber.HeaderCacheControl, "private, no-store")

	return ctx.SendStream(berkas, int(dokumen.UkuranByte))
}

// Tempel attaches an orphan to the document it belongs to.
func (c *DokumenController) Tempel(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	request := new(model.TempelDokumenRequest)
	if err := ctx.Bind().Body(request); err != nil {
		return model.Invalid("malformed request body")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	// Bound first, then overwritten: a body carrying its own id or actor cannot pick
	// which attachment is written or who is recorded as asking.
	request.ID = id
	request.ActorID = actor

	response, err := c.UseCase.Tempel(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.DokumenResponse]{Data: response})
}

func (c *DokumenController) Delete(ctx fiber.Ctx) error {
	id, err := strconv.ParseInt(ctx.Params("id"), 10, 64)
	if err != nil {
		return model.Invalid("id must be an integer")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	response, err := c.UseCase.Hapus(ctx.Context(), &model.DeleteDokumenRequest{ID: id, ActorID: actor})
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[*model.DokumenResponse]{Data: response})
}

func (c *DokumenController) List(ctx fiber.Ctx) error {
	request := new(model.ListDokumenRequest)
	if err := ctx.Bind().Query(request); err != nil {
		return model.Invalid("malformed query parameters")
	}

	actor, err := actorID(ctx)
	if err != nil {
		return err
	}

	request.ActorID = actor

	responses, paging, err := c.UseCase.Search(ctx.Context(), request)
	if err != nil {
		return err
	}

	return ctx.JSON(model.WebResponse[[]model.DokumenResponse]{Data: responses, Paging: paging})
}

// dispositionLampiran builds the Content-Disposition value for a download.
//
// nama is the client's own filename, stored untouched as display text, so it reaches
// here as arbitrary bytes. A quote or a newline in a header value is header
// injection, so the quoted form is stripped down to safe ASCII and the real name is
// carried in the RFC 5987 filename* parameter, which is percent-encoded and is what
// every current browser prefers anyway.
func dispositionLampiran(nama string) string {
	// Guarded before either form is built, so the two never disagree about what the
	// file is called. A name that is blank once whitespace is stripped — including one
	// that is nothing but a newline — has nothing worth carrying through.
	if strings.TrimSpace(nama) == "" {
		nama = "lampiran"
	}

	aman := strings.Map(func(r rune) rune {
		// Printable ASCII only, minus the two characters that end or escape a quoted
		// string. Everything else — control characters, CR, LF, and the whole of
		// non-ASCII — is replaced rather than dropped, so the name keeps its shape.
		if r < 0x20 || r > 0x7E || r == '"' || r == '\\' {
			return '_'
		}

		return r
	}, nama)

	// QueryEscape encodes a space as '+', which means a literal plus inside a
	// filename* value rather than a space.
	terkode := strings.ReplaceAll(url.QueryEscape(nama), "+", "%20")

	return `attachment; filename="` + aman + `"; filename*=UTF-8''` + terkode
}
