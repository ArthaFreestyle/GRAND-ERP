package route

import (
	deliveryhttp "Arthafreestyle/ERP/internal/delivery/http"

	"github.com/gofiber/fiber/v3"
)

// RouteConfig lists every controller the HTTP surface exposes. Add a field per
// new module and register its routes in Setup.
type RouteConfig struct {
	App *fiber.App

	// DocsController is nil when web.swagger is false, and the docs routes are then
	// not registered at all. Nil rather than a boolean flag so there is no way to
	// enable the routes without also having something to serve them.
	DocsController *deliveryhttp.DocsController

	RuangController     *deliveryhttp.RuangController
	SatuanController    *deliveryhttp.SatuanController
	EkspedisiController *deliveryhttp.EkspedisiController
	SupplierController  *deliveryhttp.SupplierController
	PelangganController *deliveryhttp.PelangganController
	RoleController      *deliveryhttp.RoleController
	UserController      *deliveryhttp.UserController
}

func (c *RouteConfig) Setup() {
	c.setupGuestRoute()
	c.setupAuthRoute()
}

// setupGuestRoute holds endpoints reachable without a session (health, docs,
// login, captcha).
func (c *RouteConfig) setupGuestRoute() {
	c.App.Get("/health", func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{"status": "ok"})
	})

	// Swagger UI at the root, reading the contract served next to it. Both are
	// skipped entirely when web.swagger is false — publishing a full API map of a
	// service that has no authentication yet is a deployment-time decision, not
	// something to discover in production.
	if c.DocsController != nil {
		c.App.Get("/", c.DocsController.UI)
		c.App.Get("/openapi.yaml", c.DocsController.Spec)
	}
}

// setupAuthRoute holds endpoints that will sit behind auth middleware once it
// exists.
//
// Master data has no DELETE by design. Every one of these tables is referenced by
// transaction tables, so deleting a row that has been used either fails on a
// foreign key or, worse, breaks the audit trail. Rows are retired with
// is_aktif = false instead.
func (c *RouteConfig) setupAuthRoute() {
	api := c.App.Group("/api/v1")

	api.Post("/ruang", c.RuangController.Create)
	api.Get("/ruang", c.RuangController.List)
	api.Get("/ruang/:id", c.RuangController.Get)

	api.Post("/satuan", c.SatuanController.Create)
	api.Get("/satuan", c.SatuanController.List)
	api.Get("/satuan/:id", c.SatuanController.Get)
	api.Patch("/satuan/:id", c.SatuanController.Update)

	api.Post("/ekspedisi", c.EkspedisiController.Create)
	api.Get("/ekspedisi", c.EkspedisiController.List)
	api.Get("/ekspedisi/:id", c.EkspedisiController.Get)
	api.Patch("/ekspedisi/:id", c.EkspedisiController.Update)

	api.Post("/supplier", c.SupplierController.Create)
	api.Get("/supplier", c.SupplierController.List)
	api.Get("/supplier/:id", c.SupplierController.Get)
	api.Patch("/supplier/:id", c.SupplierController.Update)

	api.Post("/pelanggan", c.PelangganController.Create)
	api.Get("/pelanggan", c.PelangganController.List)
	api.Get("/pelanggan/:id", c.PelangganController.Get)
	api.Patch("/pelanggan/:id", c.PelangganController.Update)

	api.Post("/role", c.RoleController.Create)
	api.Get("/role", c.RoleController.List)
	api.Get("/role/:id", c.RoleController.Get)
	api.Patch("/role/:id", c.RoleController.Update)

	// A user's roles are granted and revoked through PATCH /user/:id with a
	// role_ids array, not through a nested sub-resource: role_ids replaces the
	// whole set, and doing it in the same request as the rest of the patch keeps
	// the user row and its grants inside one transaction.
	//
	// These routes will need the tightest authorization in the system once the
	// middleware exists — creating a user and granting it SUPERADMIN is a
	// privilege escalation path.
	api.Post("/user", c.UserController.Create)
	api.Get("/user", c.UserController.List)
	api.Get("/user/:id", c.UserController.Get)
	api.Patch("/user/:id", c.UserController.Update)
}
