package http

import (
	"Arthafreestyle/ERP/docs"

	"github.com/gofiber/fiber/v3"
	"github.com/sirupsen/logrus"
)

// DocsController serves the API documentation: Swagger UI at the root and the
// OpenAPI contract it reads.
//
// There is no usecase behind it and no business logic in it — the contract is a
// build-time asset, not data — so it takes no repository and touches no database.
//
// gofiber/contrib/swagger is not used: its latest release still requires Fiber v2,
// and this project is on v3.
type DocsController struct {
	Log *logrus.Logger
}

func NewDocsController(log *logrus.Logger) *DocsController {
	return &DocsController{Log: log}
}

// specPath is where UI tells the browser to fetch the contract, and the route Spec
// is registered on. Declared once so the two cannot drift apart.
const specPath = "/openapi.yaml"

// swaggerUI is the whole docs page. Swagger UI itself is loaded from a CDN rather
// than vendored: the alternative is committing ~1.5 MB of minified third-party
// JavaScript, and this page is a development convenience that the API does not
// depend on.
//
// The trade-off is that the page needs internet access to render, while the API
// underneath does not. Pin swagger-ui-dist to an exact version instead of the major
// range if a reproducible docs page ever matters more than that convenience.
//
// swagger-ui-bundle.js is loaded without swagger-ui-standalone-preset.js on
// purpose: the preset only adds the topbar with its spec-URL input, which is noise
// when there is exactly one spec to show.
const swaggerUI = `<!DOCTYPE html>
<html lang="id">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GRAND-ERP API</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
<style>
  body { margin: 0; background: #fafafa; }
  .swagger-ui .info { margin: 24px 0; }
</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
<script>
  window.ui = SwaggerUIBundle({
    url: "` + specPath + `",
    dom_id: "#swagger-ui",
    deepLinking: true,
    tryItOutEnabled: true,
    displayRequestDuration: true,
    defaultModelsExpandDepth: 0,
  });
</script>
</body>
</html>
`

// UI serves the documentation page at the root.
func (c *DocsController) UI(ctx fiber.Ctx) error {
	ctx.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)

	return ctx.SendString(swaggerUI)
}

// Spec serves the embedded contract.
//
// "Try it out" in the page posts to whatever is listed under `servers:` in the
// contract, which is http://localhost:3000. Reaching the docs from any other host
// or port means editing that list — the spec is served verbatim and nothing here
// rewrites it.
func (c *DocsController) Spec(ctx fiber.Ctx) error {
	// application/yaml is the registered type as of RFC 9512. Browsers show it as
	// text either way; Swagger UI does not care.
	ctx.Set(fiber.HeaderContentType, "application/yaml; charset=utf-8")

	return ctx.Send(docs.OpenAPI)
}
