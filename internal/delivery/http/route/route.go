package route

import (
	deliveryhttp "Arthafreestyle/ERP/internal/delivery/http"
	"Arthafreestyle/ERP/internal/delivery/http/middleware"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/gofiber/fiber/v3"
)

// Role names, matching db/seeder_postgres/003_role.sql. Constants rather than string
// literals at each call site: role.nama can be renamed through
// PATCH /api/v1/role/{id}, and when that happens the compiler cannot help — but at
// least the names to change are all in one place.
const (
	RoleSuperadmin = "SUPERADMIN"
	RoleCashier    = "CASHIER"
	RoleInventaris = "INVENTARIS"
)

// RouteConfig lists every controller the HTTP surface exposes. Add a field per new
// module and register its routes in Setup.
type RouteConfig struct {
	App *fiber.App

	// AuthUseCase backs the authentication middleware. Required.
	AuthUseCase *usecase.AuthUseCase

	// DocsController is nil when web.swagger is false, and the docs routes are then
	// not registered at all. Nil rather than a boolean flag so there is no way to
	// enable the routes without also having something to serve them.
	DocsController *deliveryhttp.DocsController

	AuthController       *deliveryhttp.AuthController
	PembelianController  *deliveryhttp.PembelianController
	SusulanController    *deliveryhttp.PenerimaanSusulanController
	ReturController      *deliveryhttp.ReturPembelianController
	PembayaranController *deliveryhttp.PembayaranUtangController
	ProductController    *deliveryhttp.ProductController
	RuangController      *deliveryhttp.RuangController
	SatuanController     *deliveryhttp.SatuanController
	EkspedisiController  *deliveryhttp.EkspedisiController
	SupplierController   *deliveryhttp.SupplierController
	PelangganController  *deliveryhttp.PelangganController
	RoleController       *deliveryhttp.RoleController
	UserController       *deliveryhttp.UserController
}

func (c *RouteConfig) Setup() {
	c.setupGuestRoute()
	c.setupAuthRoute()
}

// setupGuestRoute holds endpoints reachable without a token: health, the docs, and
// login itself.
//
// Login cannot sit behind the auth middleware, for the obvious reason. Health stays
// open so a container healthcheck does not need a credential.
func (c *RouteConfig) setupGuestRoute() {
	c.App.Get("/health", func(ctx fiber.Ctx) error {
		return ctx.JSON(fiber.Map{"status": "ok"})
	})

	// Swagger UI at the root, reading the contract served next to it. Both are
	// skipped entirely when web.swagger is false — publishing a full API map of a
	// service is a deployment-time decision, not something to discover in
	// production.
	if c.DocsController != nil {
		c.App.Get("/", c.DocsController.UI)
		c.App.Get("/openapi.yaml", c.DocsController.Spec)
	}

	c.App.Post("/api/v1/auth/login", c.AuthController.Login)
}

// setupAuthRoute holds everything behind a bearer token.
//
// Master data has no DELETE by design. Every one of these tables is referenced by
// transaction tables, so deleting a row that has been used either fails on a foreign
// key or, worse, breaks the audit trail. Rows are retired with is_aktif = false
// instead.
//
// Authorization policy, in one place so it can be read as a whole:
//
//   - Reads are open to any authenticated user. An operator who cannot see a
//     supplier cannot do their job, whatever their role.
//   - Writes are split by who owns the data: INVENTARIS keeps goods, units, rooms,
//     carriers, and suppliers; CASHIER keeps customers.
//   - SUPERADMIN may do anything, so it appears in every write guard.
//   - user and role are SUPERADMIN-only, reads included. Listing accounts and their
//     privileges is itself sensitive, and being able to write there is a privilege
//     escalation path: grant yourself SUPERADMIN and the rest follows.
//   - pembelian, penerimaan-susulan, and retur-pembelian split along their approval
//     flow rather than by module: see the comment above those routes.
//
// This split is a starting assumption drawn from the three role names, not something
// derived from a spec. Adjust the guards as the real division of work becomes clear.
func (c *RouteConfig) setupAuthRoute() {
	api := c.App.Group("/api/v1", middleware.NewAuth(c.AuthUseCase))

	// Any authenticated caller may ask who the server thinks they are.
	api.Get("/auth/me", c.AuthController.Me)

	// Guard first, controller last. Fiber v3's signature is
	// Get(path, handler, handlers...) and the chain runs in the order given, so the
	// role check has to be the FIRST argument. Putting it last registers a guard that
	// runs after the controller has already written its response — which is to say,
	// never, since a controller does not call Next(). TestRouteGuardsRunBeforeHandler
	// pins this ordering.
	inventaris := middleware.RequireRole(RoleSuperadmin, RoleInventaris)
	cashier := middleware.RequireRole(RoleSuperadmin, RoleCashier)
	superadmin := middleware.RequireRole(RoleSuperadmin)

	api.Get("/ruang", c.RuangController.List)
	api.Get("/ruang/:id", c.RuangController.Get)
	api.Post("/ruang", inventaris, c.RuangController.Create)

	// product writes sit with INVENTARIS: it is goods master data. Selling prices are
	// grouped here too rather than with CASHIER, because product_harga_jual only feeds
	// the default price on an input screen — the price actually charged is a snapshot
	// on penjualan_detail, which is a different module's decision.
	api.Get("/product", c.ProductController.List)
	api.Get("/product/:id", c.ProductController.Get)
	api.Post("/product", inventaris, c.ProductController.Create)
	api.Patch("/product/:id", inventaris, c.ProductController.Update)
	api.Post("/product/:id/satuan", inventaris, c.ProductController.AddSatuan)
	api.Post("/product/:id/harga-jual", inventaris, c.ProductController.AddHargaJual)

	// riwayat-beli is a read, so it follows the read rule and is open to any
	// authenticated caller. It is the replacement for a purchase order: what an
	// operator needs before ordering is the price last paid, and it reads only
	// documents that were posted anyway.
	api.Get("/product/:id/riwayat-beli", c.ProductController.RiwayatBeli)

	// pembelian is the first module whose writes are split by workflow stage rather
	// than by which data they touch, because posting one is not an edit — it appends
	// to kartu_stok, which is append-only. A wrong posting cannot be corrected, only
	// reversed, and the reversal is valued at whatever the moving average has become
	// by then. So the desk that reads the paper invoice and counts the box is not the
	// desk that decides those numbers may enter the stock ledger.
	//
	// INVENTARIS types the document and submits it; SUPERADMIN approves, rejects, or
	// voids. The split is on the transition, not the record: the same person may well
	// hold both roles in a small office, and then nothing changes for them.
	api.Get("/pembelian", c.PembelianController.List)
	api.Get("/pembelian/:id", c.PembelianController.Get)
	api.Get("/pembelian/:id/sisa", c.PembelianController.Sisa)
	api.Post("/pembelian", inventaris, c.PembelianController.Create)
	api.Patch("/pembelian/:id", inventaris, c.PembelianController.Update)
	api.Put("/pembelian/:id/detail", inventaris, c.PembelianController.ReplaceDetail)
	api.Post("/pembelian/:id/bagi-rata-koli", inventaris, c.PembelianController.BagiRataKoli)
	api.Post("/pembelian/:id/ajukan", inventaris, c.PembelianController.Ajukan)
	api.Post("/pembelian/:id/posting", superadmin, c.PembelianController.Posting)
	api.Post("/pembelian/:id/tolak", superadmin, c.PembelianController.Tolak)
	api.Post("/pembelian/:id/batal", superadmin, c.PembelianController.Batal)

	// penerimaan-susulan carries the same split for the same reason: it writes
	// kartu_stok too. Nothing here adds to what the supplier is owed — the invoice
	// was booked in full with the first delivery — so the approval is about stock,
	// not about money.
	api.Get("/penerimaan-susulan", c.SusulanController.List)
	api.Get("/penerimaan-susulan/:id", c.SusulanController.Get)
	api.Post("/penerimaan-susulan", inventaris, c.SusulanController.Create)
	api.Patch("/penerimaan-susulan/:id", inventaris, c.SusulanController.Update)
	api.Put("/penerimaan-susulan/:id/detail", inventaris, c.SusulanController.ReplaceDetail)
	api.Post("/penerimaan-susulan/:id/ajukan", inventaris, c.SusulanController.Ajukan)
	api.Post("/penerimaan-susulan/:id/posting", superadmin, c.SusulanController.Posting)
	api.Post("/penerimaan-susulan/:id/tolak", superadmin, c.SusulanController.Tolak)
	api.Post("/penerimaan-susulan/:id/batal", superadmin, c.SusulanController.Batal)

	// retur-pembelian carries the same split, and here the case for it is strongest:
	// this is the one document so far whose posting takes goods *out* of stock, so a
	// wrong one can drive a balance to a figure that no longer matches the shelf, and
	// its reversal is valued at whatever the moving average has become. Nothing here
	// reduces what the supplier is owed either — the credit note is settled with them on
	// paper, and the payable side is fase 6.
	api.Get("/retur-pembelian", c.ReturController.List)
	api.Get("/retur-pembelian/:id", c.ReturController.Get)
	api.Post("/retur-pembelian", inventaris, c.ReturController.Create)
	api.Patch("/retur-pembelian/:id", inventaris, c.ReturController.Update)
	api.Put("/retur-pembelian/:id/detail", inventaris, c.ReturController.ReplaceDetail)
	api.Post("/retur-pembelian/:id/ajukan", inventaris, c.ReturController.Ajukan)
	api.Post("/retur-pembelian/:id/posting", superadmin, c.ReturController.Posting)
	api.Post("/retur-pembelian/:id/tolak", superadmin, c.ReturController.Tolak)
	api.Post("/retur-pembelian/:id/batal", superadmin, c.ReturController.Batal)

	// pembayaran-utang is the first module that touches no stock at all, so the reason the
	// three above split by workflow stage does not apply to it — nothing it writes is
	// append-only, and voiding it recomputes every cache exactly. It still splits, for a
	// different reason: this is money leaving the bank. CASHIER prepares the document
	// because it is the money desk; SUPERADMIN releases it, voids it, and decides what
	// became of a giro.
	//
	// CASHIER rather than INVENTARIS is a judgement call and the least settled guard in
	// this table — supplier payments arguably belong to a PURCHASING or FINANCE role that
	// does not exist yet. Isu #4 raises exactly that question and leaves it open.
	api.Get("/pembayaran-utang", c.PembayaranController.List)
	api.Get("/pembayaran-utang/:id", c.PembayaranController.Get)
	api.Post("/pembayaran-utang", cashier, c.PembayaranController.Create)
	api.Patch("/pembayaran-utang/:id", cashier, c.PembayaranController.Update)
	api.Put("/pembayaran-utang/:id/alokasi", cashier, c.PembayaranController.ReplaceAlokasi)
	api.Post("/pembayaran-utang/:id/posting", superadmin, c.PembayaranController.Posting)
	api.Post("/pembayaran-utang/:id/batal", superadmin, c.PembayaranController.Batal)
	api.Post("/pembayaran-utang/:id/cair", superadmin, c.PembayaranController.Cairkan)
	api.Post("/pembayaran-utang/:id/tolak-giro", superadmin, c.PembayaranController.TolakGiro)

	api.Get("/satuan", c.SatuanController.List)
	api.Get("/satuan/:id", c.SatuanController.Get)
	api.Post("/satuan", inventaris, c.SatuanController.Create)
	api.Patch("/satuan/:id", inventaris, c.SatuanController.Update)

	api.Get("/ekspedisi", c.EkspedisiController.List)
	api.Get("/ekspedisi/:id", c.EkspedisiController.Get)
	api.Post("/ekspedisi", inventaris, c.EkspedisiController.Create)
	api.Patch("/ekspedisi/:id", inventaris, c.EkspedisiController.Update)

	api.Get("/supplier", c.SupplierController.List)
	api.Get("/supplier/:id", c.SupplierController.Get)

	// utang is a read, so it follows the read rule and is open to any authenticated caller.
	// Like riwayat-beli it is the answer falling out of documents that were posted anyway:
	// which of this supplier's invoices are still open, and for how much.
	api.Get("/supplier/:id/utang", c.SupplierController.Utang)
	api.Post("/supplier", inventaris, c.SupplierController.Create)
	api.Patch("/supplier/:id", inventaris, c.SupplierController.Update)

	api.Get("/pelanggan", c.PelangganController.List)
	api.Get("/pelanggan/:id", c.PelangganController.Get)
	api.Post("/pelanggan", cashier, c.PelangganController.Create)
	api.Patch("/pelanggan/:id", cashier, c.PelangganController.Update)

	api.Get("/role", superadmin, c.RoleController.List)
	api.Get("/role/:id", superadmin, c.RoleController.Get)
	api.Post("/role", superadmin, c.RoleController.Create)
	api.Patch("/role/:id", superadmin, c.RoleController.Update)

	// A user's roles are granted and revoked through PATCH /user/:id with a role_ids
	// array, not through a nested sub-resource: role_ids replaces the whole set, and
	// doing it in the same request as the rest of the patch keeps the user row and
	// its grants inside one transaction.
	api.Get("/user", superadmin, c.UserController.List)
	api.Get("/user/:id", superadmin, c.UserController.Get)
	api.Post("/user", superadmin, c.UserController.Create)
	api.Patch("/user/:id", superadmin, c.UserController.Update)
}
