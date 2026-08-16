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
	DokumenController    *deliveryhttp.DokumenController
	PeriodeController    *deliveryhttp.PeriodeController
	PembelianController  *deliveryhttp.PembelianController
	SusulanController    *deliveryhttp.PenerimaanSusulanController
	ReturController      *deliveryhttp.ReturPembelianController
	MutasiController     *deliveryhttp.MutasiController
	PemakaianController  *deliveryhttp.PemakaianController
	PenjualanController  *deliveryhttp.PenjualanController
	PembayaranController *deliveryhttp.PembayaranUtangController
	ProductController    *deliveryhttp.ProductController
	UnitKerjaController  *deliveryhttp.UnitKerjaController
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
//     supplier cannot do their job, whatever their role. That is a statement about
//     the ROUTE GUARD, not the whole answer a read gives: since isu #12 fase 6, ruang,
//     pembelian, penerimaan-susulan, retur-pembelian, mutasi, and product's
//     stok-per-ruang read additionally scope by the caller's active unit_kerja —
//     a room, document, or balance outside it is silently omitted from a list, or
//     answers 404 on a Get, as if it did not exist. That scoping is enforced in the
//     usecase layer, from the session's active grant, not by a route guard here, so
//     it does not appear in this route table at all. A caller with a global grant
//     (nil active unit — the SUPERADMIN shape) is unrestricted, same as always.
//   - Writes are split by who owns the data: INVENTARIS keeps goods, units of
//     measure, work units (unit_kerja), rooms, carriers, and suppliers; CASHIER
//     keeps customers.
//   - SUPERADMIN may do anything, so it appears in every write guard.
//   - user and role are SUPERADMIN-only, reads included. Listing accounts and their
//     privileges is itself sensitive, and being able to write there is a privilege
//     escalation path: grant yourself SUPERADMIN and the rest follows.
//   - pembelian, penerimaan-susulan, and retur-pembelian split along their approval
//     flow rather than by module: see the comment above those routes.
//   - mutasi writes kartu_stok but has no approval flow, so the split lives here alone
//     and nowhere else: INVENTARIS reaches DRAFT, SUPERADMIN posts and voids.
//   - periode writes are SUPERADMIN-only. Closing a month is not this module's own
//     data changing — it is every other module losing the ability to post into it.
//
// This split is a starting assumption drawn from the three role names, not something
// derived from a spec. Adjust the guards as the real division of work becomes clear.
func (c *RouteConfig) setupAuthRoute() {
	api := c.App.Group("/api/v1", middleware.NewAuth(c.AuthUseCase))

	// Any authenticated caller may ask who the server thinks they are.
	api.Get("/auth/me", c.AuthController.Me)

	// switch-context (isu #12 fase 4) carries no role guard, the same as
	// auth/me: a session with no active context (issued at login when the
	// caller holds more than one grant) authorizes nothing at all, so it
	// cannot reach anything guarded by RequireRole regardless — that is
	// Session.HasRole answering false when Aktif is nil, not a route-table
	// special case. Nothing below this line changed for fase 4.
	api.Post("/auth/switch-context", c.AuthController.SwitchContext)

	// Guard first, controller last. Fiber v3's signature is
	// Get(path, handler, handlers...) and the chain runs in the order given, so the
	// role check has to be the FIRST argument. Putting it last registers a guard that
	// runs after the controller has already written its response — which is to say,
	// never, since a controller does not call Next(). TestRouteGuardsRunBeforeHandler
	// pins this ordering.
	inventaris := middleware.RequireRole(RoleSuperadmin, RoleInventaris)
	cashier := middleware.RequireRole(RoleSuperadmin, RoleCashier)
	superadmin := middleware.RequireRole(RoleSuperadmin)

	// dokumen is infrastructure rather than a module: attachments are needed by
	// receiving, by returns, by delivery notes, and by stock counts, so it belongs to
	// none of them and carries no role guard of its own beyond being authenticated.
	//
	// That is not the reads-are-open rule being stretched to cover writes. What
	// protects an attachment is its state, not the caller's role: an upload is inert
	// until something claims it, attaching is refused once a document is voided or
	// already carries ten files, and removal is refused the moment the parent leaves
	// DRAFT. A role split would say who may photograph an invoice, which is a question
	// the receiving desk answers by standing there.
	//
	// Downloads stay behind the token like everything else. A photographed invoice
	// carries purchase prices and a supplier's identity, which is exactly what an API
	// should not hand out to an unauthenticated caller with a guessable id.
	api.Post("/dokumen", c.DokumenController.Upload)
	api.Get("/dokumen", c.DokumenController.List)
	api.Get("/dokumen/:id", c.DokumenController.Isi)
	api.Post("/dokumen/:id/tempel", c.DokumenController.Tempel)
	// The only DELETE in the API. Master data has none by design, and this one is a
	// soft delete: the row survives with deleted_at set, so the trace of an upload
	// outlives the file.
	api.Delete("/dokumen/:id", c.DokumenController.Delete)

	// periode is book closing, and its guard is the strictest in this table: closing a
	// month is what stops every document type from posting into it, now and for every
	// module that writes kartu_stok later. Reopening is SUPERADMIN too — a month anyone
	// can reopen was never really closed, and the two act as one control.
	//
	// Reads stay open like every other read. Whether last month is still open is
	// exactly what an operator needs to know before typing a late invoice.
	//
	// Keyed on (tahun, bulan) instead of /{id}, unlike every other module. That pair is
	// the real identity — periode_tahun_bulan_uidx says so — and an id-keyed route could
	// not address the ordinary case at all, since a month nobody has closed has no row
	// and so no id. The action endpoints still follow the documents' POST /{...}/aksi
	// shape.
	api.Get("/periode", c.PeriodeController.List)
	api.Get("/periode/:tahun/:bulan", c.PeriodeController.Get)
	api.Post("/periode/:tahun/:bulan/tutup", superadmin, c.PeriodeController.Tutup)
	api.Post("/periode/:tahun/:bulan/buka", superadmin, c.PeriodeController.Buka)

	// unit_kerja is isu #12 fase 2: the organizational location a ruang belongs
	// to. It is plain master data — no number, no lines, no posting — so it
	// follows the same split as ruang and carries a PATCH, unlike ruang.
	api.Get("/unit-kerja", c.UnitKerjaController.List)
	api.Get("/unit-kerja/:id", c.UnitKerjaController.Get)
	api.Post("/unit-kerja", inventaris, c.UnitKerjaController.Create)
	api.Patch("/unit-kerja/:id", inventaris, c.UnitKerjaController.Update)

	// ruang.id_unit_kerja is required and validated active at create time (isu
	// #12 fase 2), but ruang still has no PATCH, so a room cannot change unit
	// through the API yet.
	api.Get("/ruang", c.RuangController.List)
	api.Get("/ruang/:id", c.RuangController.Get)
	api.Post("/ruang", inventaris, c.RuangController.Create)

	// product writes sit with INVENTARIS: it is goods master data. Selling prices are
	// grouped here too rather than with CASHIER, because product_harga_jual only feeds
	// the default price on an input screen — the price actually charged is a snapshot
	// on penjualan_detail, which is a different module's decision.
	//
	// /product/harga-jual (isu #8 fase 3) is registered ahead of /product/:id so the
	// literal segment cannot be swallowed by the :id parameter — Fiber's router
	// prefers a static match at the same position regardless of order, but naming the
	// order here removes any doubt.
	api.Get("/product", c.ProductController.List)
	api.Get("/product/harga-jual", c.ProductController.DaftarHargaJual)
	api.Get("/product/:id", c.ProductController.Get)
	api.Post("/product", inventaris, c.ProductController.Create)
	api.Patch("/product/:id", inventaris, c.ProductController.Update)
	api.Post("/product/:id/satuan", inventaris, c.ProductController.AddSatuan)

	// harga-jual (isu #8): GET resolves which version is in force, open to any
	// authenticated caller like every other read; POST/PATCH/DELETE mutate the price
	// list and sit with INVENTARIS same as the rest of this block. PATCH and DELETE
	// are refused once a penjualan_detail line references the version — enforced in
	// the usecase, not the route.
	api.Get("/product/:id/harga-jual", c.ProductController.HargaBerlaku)
	api.Post("/product/:id/harga-jual", inventaris, c.ProductController.AddHargaJual)
	api.Patch("/product/:id/harga-jual/:id_harga", inventaris, c.ProductController.UpdateHargaJual)
	api.Delete("/product/:id/harga-jual/:id_harga", inventaris, c.ProductController.DeleteHargaJual)

	// riwayat-beli is a read, so it follows the read rule and is open to any
	// authenticated caller. It is the replacement for a purchase order: what an
	// operator needs before ordering is the price last paid, and it reads only
	// documents that were posted anyway.
	api.Get("/product/:id/riwayat-beli", c.ProductController.RiwayatBeli)

	// stok is a read of kartu_stok and follows the same rule. It is what an input screen
	// needs before anyone can type a document that takes goods out: mutasi cannot pick a
	// source room without it, and penjualan, pemakaian, and stok opname will want the
	// same answer. Being able to see it is not the same as being able to move it — the
	// documents that do carry their own guards.
	api.Get("/product/:id/stok", c.ProductController.Stok)

	// pos/product is the POS catalog screen — isu #11 — and a read, so it follows the
	// same open rule. It is its own path rather than a `view` parameter on
	// GET /product: the response shape a single screen dictates should be free to
	// change with that screen without shaking the plain product contract, and it
	// also cannot collide with /product/:id — GET /product/pos would depend on
	// registration order, and getting that order wrong would hand "pos" to the
	// controller as if it were an id. HPP never appears in this response even though
	// the read is open to any authenticated caller — that is enforced in the usecase
	// and the model, not by a guard here.
	api.Get("/pos/product", c.ProductController.POS)

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

	// mutasi writes kartu_stok too, and twice per line, but it is the one such module
	// whose guards do NOT split along an approval flow — because it has none. There is no
	// DIAJUKAN and so no ajukan or tolak endpoint: a wrong transfer records goods in the
	// wrong room, and total stock and total inventory value do not move at all. No outside
	// party, no money, and the correction is another transfer the same person may write.
	//
	// So the split moves entirely here, and this table becomes the only control there is.
	// INVENTARIS reaches DRAFT and no further; SUPERADMIN posts and voids. Same shape as
	// pembayaran-utang — one desk prepares, another releases — one state fewer.
	//
	// The consequence to know about: with no DIAJUKAN there is no "this draft is ready"
	// signal, so DRAFT means both "still being typed" and "please post it". The list
	// endpoint filtered to status=DRAFT with terlama_dulu=true is what stands in for the
	// queue. If that turns out not to be enough, the fix is to add DIAJUKAN — a cheaper
	// change than removing it would have been.
	api.Get("/mutasi", c.MutasiController.List)
	api.Get("/mutasi/:id", c.MutasiController.Get)
	api.Post("/mutasi", inventaris, c.MutasiController.Create)
	api.Patch("/mutasi/:id", inventaris, c.MutasiController.Update)
	api.Put("/mutasi/:id/detail", inventaris, c.MutasiController.ReplaceDetail)
	api.Post("/mutasi/:id/posting", superadmin, c.MutasiController.Posting)
	api.Post("/mutasi/:id/batal", superadmin, c.MutasiController.Batal)

	// pemakaian is the fifth module to write kartu_stok, and the first to take goods
	// out with no counterparty at all — no supplier, no customer, no destination room.
	// It keeps DIAJUKAN, unlike mutasi, and adds a further DISETUJUI stage on top of
	// it: approval decides HOW MUCH may leave, posting records that it actually did.
	// INVENTARIS types the request and submits it; SUPERADMIN approves, rejects,
	// posts, and voids — the same split every other kartu_stok writer uses, along the
	// workflow stage rather than the data.
	api.Get("/pemakaian", c.PemakaianController.List)
	api.Get("/pemakaian/:id", c.PemakaianController.Get)
	api.Post("/pemakaian", inventaris, c.PemakaianController.Create)
	api.Patch("/pemakaian/:id", inventaris, c.PemakaianController.Update)
	api.Put("/pemakaian/:id/detail", inventaris, c.PemakaianController.ReplaceDetail)
	api.Post("/pemakaian/:id/ajukan", inventaris, c.PemakaianController.Ajukan)
	api.Post("/pemakaian/:id/setujui", superadmin, c.PemakaianController.Setujui)
	api.Post("/pemakaian/:id/tolak", superadmin, c.PemakaianController.Tolak)
	api.Post("/pemakaian/:id/posting", superadmin, c.PemakaianController.Posting)
	api.Post("/pemakaian/:id/batal", superadmin, c.PemakaianController.Batal)

	// penjualan is the sixth module to write kartu_stok, and the first whose goods
	// leave to an outside party with money moving on the other side — isu #10. It
	// keeps no DIAJUKAN, the same shape mutasi uses, but for a different reason: a
	// cashier cannot make a customer wait at the counter for a supervisor to
	// approve a cash sale typed in seconds. So the two-person control moves
	// entirely to the cancellation side instead of the posting side — CASHIER
	// creates, types lines, and posts a nota in one motion; SUPERADMIN alone may
	// void one. A mistyped line is corrected with a retur_penjualan (once that
	// module exists) or a cancellation by a supervisor, never by the cashier who
	// posted it.
	api.Get("/penjualan", c.PenjualanController.List)
	api.Get("/penjualan/:id", c.PenjualanController.Get)
	api.Post("/penjualan", cashier, c.PenjualanController.Create)
	api.Patch("/penjualan/:id", cashier, c.PenjualanController.Update)
	api.Put("/penjualan/:id/detail", cashier, c.PenjualanController.ReplaceDetail)
	api.Post("/penjualan/:id/posting", cashier, c.PenjualanController.Posting)
	api.Post("/penjualan/:id/batal", superadmin, c.PenjualanController.Batal)

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

	// piutang is a read, so it follows the read rule and is open to any
	// authenticated caller — isu #10 fase 2, the receivable-side mirror of
	// GET /supplier/:id/utang. Like riwayat-beli and utang it is the answer
	// falling out of documents that were posted anyway: which of this customer's
	// KREDIT notas are still open, and for how much.
	api.Get("/pelanggan/:id/piutang", c.PelangganController.Piutang)
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
