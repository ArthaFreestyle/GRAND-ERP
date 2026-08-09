package route

import (
	deliveryhttp "Arthafreestyle/ERP/internal/delivery/http"

	"github.com/gofiber/fiber/v3"
)

// RouteConfig lists every controller the HTTP surface exposes. Add a field per
// new module and register its routes in Setup.
type RouteConfig struct {
	App             *fiber.App
	RuangController *deliveryhttp.RuangController
}

func (c *RouteConfig) Setup() {
	c.setupGuestRoute()
	c.setupAuthRoute()
}

// setupGuestRoute holds endpoints reachable without a session (health, login,
// captcha).
func (c *RouteConfig) setupGuestRoute() {
	c.App.Get("/health", func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{"status": "ok"})
	})
}

// setupAuthRoute holds endpoints that will sit behind auth middleware once it
// exists.
func (c *RouteConfig) setupAuthRoute() {
	api := c.App.Group("/api/v1")

	api.Post("/ruang", c.RuangController.Create)
	api.Get("/ruang", c.RuangController.List)
	api.Get("/ruang/:id", c.RuangController.Get)
}
