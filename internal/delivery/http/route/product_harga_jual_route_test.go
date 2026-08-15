package route

// Isu #8 fase 3 registers /product/harga-jual next to /product/:id, and the comment
// above that registration in route.go claims Fiber resolves the literal segment
// first regardless of order. This pins that claim against Fiber's actual router
// rather than leaving it as an assertion nobody checked — a router change that ever
// made :id win here would silently route the price list into ProductController.Get
// with "harga-jual" parsed as an id.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestStaticSegmentBeatsParamAtSamePosition(t *testing.T) {
	app := fiber.New()

	app.Get("/product/harga-jual", func(c fiber.Ctx) error {
		return c.SendString("daftar-harga")
	})
	app.Get("/product/:id", func(c fiber.Ctx) error {
		return c.SendString("get:" + c.Params("id"))
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/product/harga-jual", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()

	body := make([]byte, 32)
	n, _ := response.Body.Read(body)

	if got := string(body[:n]); got != "daftar-harga" {
		t.Fatalf("GET /product/harga-jual dispatched to %q, want the static route — "+
			"the :id route would have swallowed \"harga-jual\" as an id", got)
	}
}
