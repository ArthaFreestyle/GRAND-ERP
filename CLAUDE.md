# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Master data is implemented, and **`pembelian` is the first transaction document** — the first thing that writes `kartu_stok`. Copy an existing slice when adding a module — don't invent a new shape.

- Implemented: **`satuan`**, **`ekspedisi`**, **`supplier`**, **`pelanggan`**, **`role`**, **`user`** (create / get / list / patch), **`unit_kerja`** (create / get / list / patch), **`ruang`** (create / get / list, no patch — now requires an active `id_unit_kerja`), **`periode`** (list / get / tutup / buka), **`product`** with `product_satuan` + `product_harga_jual`, **`pembelian`** with `pembelian_detail`, `kartu_stok` posting, and `document_counter`, **`penerimaan_susulan`** with `penerimaan_susulan_detail`, **`retur_pembelian`** with `retur_pembelian_detail`, **`pembayaran_utang`** with `pembayaran_utang_alokasi`, **`mutasi`** with `mutasi_detail`, **`dokumen`** (file attachments, and the only module holding a store that is not the database), and three queries that are not modules — **`riwayat_beli`**, **`utang_supplier`**, and **`stok_per_ruang`**
- **`unit_kerja` (isu #12) has all five load-bearing phases plus the optional read-scoping piece of phase 6** — the master table, `ruang.id_unit_kerja`, `user_role.id_unit_kerja` (grants scoped to a unit), active session context (`POST /api/v1/auth/switch-context`), `id_ruang` validated against the active unit on `pembelian`/`mutasi` writes, and reads on `ruang`, `pembelian`, `penerimaan_susulan`, `retur_pembelian`, `mutasi`, and `product/{id}/stok` scoped to the same active unit. See "Unit kerja and location-bound authority", "Wewenang bertempat", "Konteks aktif per sesi", "`id_ruang` validated against the active unit", and "Read-path scoping" below before touching any of it. `users.id_ruang_default` and a role-as-snapshot column remain deferred
- Use **`supplier`** as the template for a plain master slice: it is the one with every ordinary concern at once — nullable unique `kode`, PATCH presence semantics, and a `LEFT JOIN` in the list query
- Use **`user`** as the template when a slice writes two tables: a user and its `user_role` grants in one transaction, and it is where `Optional[[]model.GrantRequest]`, bcrypt hashing, and the `Touch` pattern live. See "Users and roles" below
- Use **`pembelian`** as the template for a **transaction document** — a state machine, a generated number, exact decimal arithmetic, and stock movements, all in one transaction. See "Pembelian and the posting engine" below. Do not model a transaction document on a master slice; the concerns barely overlap
- Use **`riwayat_beli`** as the template for a **read that is not a module** — no table, no migration, one query in another module's repository, borrowed by the usecase that owns the resource. See "Riwayat harga beli" below
- Use **`penerimaan_susulan`** as the template for a **document that derives from another** — one that points at a parent's detail rows, draws down a quota held there, copies a cost snapshot from it, and rewrites a cache on it. See "Penerimaan susulan" below
- **`retur_pembelian` is that same template with the goods moving the other way**, and the two are worth reading side by side: what differs between them is only the direction, and every consequence of that difference is called out in "Retur pembelian" below. It is also the only module so far whose posting takes stock *out*
- Use **`mutasi`** as the template for a document that moves stock **in two directions at once**, and as the only stock-writing module with **no approval stage**. It is also where balance reads and canonical lock ordering live. See "Mutasi antar ruang" below
- Use **`dokumen`** as the template for a slice holding a **store that is not PostgreSQL** — the storage interface, the ordering between a file and its row, and the worker job that reconciles them. It is also the only module with a polymorphic reference and the only one with a `DELETE`. See "Dokumen and file attachments" below
- Use **`pembayaran_utang`** as the template for a **transaction document that touches no stock**. It is the only one, and the absence is what shapes it: no approval state, no `kartu_stok`, and caches that can be recomputed exactly rather than reversed. See "Pembayaran utang and the payable side" below
- Use **`periode`** as the template for a slice keyed on something other than an `id`, and as the one place a **cross-cutting refusal** lives. It is master-data shaped — no number, no lines, no posting — but what it writes changes what every other module may do, and the enforcement is a trigger rather than a call. See "Periode and book closing" below
- Module path: `Arthafreestyle/ERP` (no domain prefix); internal imports are `Arthafreestyle/ERP/internal/...`
- Go 1.25.0 — required by Fiber v3.4.0, which refuses to build on 1.24
- The rest of the inventory/sales schema exists as migrations `000002`–`000008` and still has **no Go layers** — sales, sales returns, `pemakaian`, stock opname, and the receivable side. See "Inventory data model" below. The posting engine itself is finished: `mutasi` was the last shape it was missing
- Auth is implemented: bearer JWT login, role guards per route, active session context (isu #12 fase 4) — see "Authentication and authorization" below. `pembelian` and `mutasi` also validate a write's `id_ruang` against that active context (fase 5), and `ruang`/`pembelian`/`penerimaan_susulan`/`retur_pembelian`/`mutasi`/`product` scope their reads by it too (fase 6)
- **`cmd/worker` now has one job**, the orphan-attachment sweep. The scheduler is a `time.Ticker` per job in `internal/config/worker.go`, wired by `BootstrapWorker` — the counterpart of `Bootstrap`, so the "wiring happens only in `internal/config`" rule still holds
- Not built yet: captcha (Redis is wired but unused), logout/refresh, session revocation (stateless tokens cannot be revoked)
- `product` and `pembelian` are the only modules that fill `created_by`/`updated_by`, taken from the token via `middleware.SessionFrom`. Every other slice still writes `NULL` — the plumbing exists, they just don't use it yet

## Stack

- Go + [Fiber v3](https://github.com/gofiber/fiber)
- PostgreSQL via `database/sql` + `jackc/pgx/v5` (registered through `pgx/v5/stdlib`) — **no ORM**; write SQL by hand
- Redis — captcha sessions with TTL
- Config/logging: viper + logrus
- Migrations: [golang-migrate](https://github.com/golang-migrate/migrate)

## Commands

```bash
go run ./cmd/web                 # run the HTTP server
go run ./cmd/worker              # run the background worker
go build ./...                   # build everything
go vet ./...                     # vet
gofmt -l .                       # list unformatted files — see the CRLF caveat below

go test ./...                                        # all tests
go test ./internal/usecase/...                       # one package
go test ./internal/usecase -run TestSatuanCreate     # one test (regex match)
go test -v -race ./...                               # verbose + race detector
```

**`gofmt -l .` is not a usable signal on a Windows checkout.** Git's `core.autocrlf=true` writes CRLF into the working tree while the index holds LF, and gofmt wants LF — so it currently lists ~74 of ~90 files. Nothing is actually misformatted; `git ls-files --eol` shows `i/lf` for all of them, and `gofmt -d` on one shows every line replaced by itself. Do not "fix" this with `gofmt -w .`. To get a real answer, check the files you touched individually, or add a `.gitattributes` with `*.go text eol=lf` and re-checkout.

Docker is the shortest path to a working stack — it brings up PostgreSQL, Redis, migrations, seeders, the server, and the worker, in that enforced order:

```bash
docker compose up -d --build     # whole stack
docker compose up -d --build web # rebuild after a Go change
docker compose down -v           # stop and discard data (also re-triggers docker/initdb/)
```

Two things about the Docker setup that will bite otherwise:

- **PostgreSQL is published on host port `5433`**, not 5432, because a locally installed PostgreSQL usually already owns 5432. Inside the compose network it is still `postgres:5432`. So `TEST_DATABASE_URL` against the container uses `127.0.0.1:5433`.
- **The `seed` service lists each seeder file by name** rather than globbing the directory. A new seeder that is not added to that list never runs under Docker.

Compose also creates and migrates `grand_erp_test`, so the database-backed tests need no manual setup:

```bash
docker compose up -d
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5433/grand_erp_test?sslmode=disable'
go test ./...
```

Tests in `internal/usecase` run against a **real PostgreSQL** and skip themselves unless `TEST_DATABASE_URL` points at a scratch database — most of what they assert (pagination stability under duplicate names, `ILIKE` wildcard escaping, many rows sharing `kode = NULL`, `NUMERIC` round-tripping, several users holding the same role) lives in the database, where a mock would just agree with a wrong query. Migrate the scratch database first; the tests clear the master tables themselves but do not create the schema.

`truncateMaster` in `internal/usecase/main_test.go` deletes in dependency order: `kartu_stok` first, then the documents that point at `pembelian_detail` (`penerimaan_susulan*`, `retur_pembelian*`) and the ones that point at `pembelian` (`pembayaran_utang*`), then `mutasi*` (which points at no document, only at `product`, `ruang`, `satuan`, and `users`), then the purchase tables, then `dokumen`, then master tables, then `user_role`, then `users`, then `role`. It uses `DELETE`, not `TRUNCATE` — `TRUNCATE` would cascade into `kartu_stok`, whose guard trigger raises on it. Add new tables to that list, on the correct side — `users` has to come after anything whose `created_by` references it.

`kartu_stok` refuses `DELETE` too, by the same append-only trigger, so `truncateMaster` disables `kartu_stok_append_only` for the length of the wipe and re-enables it in a `defer`. That is a licence the scratch database gets and nothing else does — if you find yourself reaching for it outside `truncateMaster`, you are about to defeat the guarantee the whole valuation rests on.

The tests live in package `usecase_test` (external), and every one of them starts with `newApp(t)`, which calls `requireDB` + `truncateMaster` and then wires the same graph `config.Bootstrap` does, minus Fiber. A new usecase needs a field on that `app` struct, or its tests have nothing to call.

**A green `go test ./...` without `TEST_DATABASE_URL` means skipped, not passed.** The whole package skips itself. Run with `-v` when you need to know which.

```bash
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/grand_erp_test?sslmode=disable'
migrate -path db/migrations_postgres -database "$TEST_DATABASE_URL" up
go test ./...
```

Everything outside `internal/usecase` is a pure unit test and needs no database.

Config comes from `config.json` in the working directory, and **any key can be overridden by an env var** with `.` replaced by `_` — `DATABASE_HOST`, `WEB_PORT`, `REDIS_PASSWORD`. `config.json` is gitignored; the tracked file is `config.example.json`, so **any new config key must be added to the example too** or a fresh clone silently loses it.

`NewViper` **panics** when `config.json` is missing — env vars alone are not enough to boot. That is why the Dockerfile copies `config.example.json` to `config.json` inside the image, and compose then overrides the environment-dependent keys. The ones compose does not set (`app.name`, `captcha.ttl_seconds`, `database.pool.*`) fall back to the baked example's values. `.dockerignore` excludes the real `config.json` so local credentials are never baked into a layer.

A new config key therefore has three homes, not one: `config.example.json`, and — if a container needs a non-default value — the `web` and `worker` `environment:` blocks in `docker-compose.yml`.

`jwt.secret` is the exception to "add it to the example": it is present there but **empty**, because a shipped signing key is worse than a missing one. `docker-compose.yml` supplies a clearly-labelled dev value so `up` works out of the box.

Migrations (golang-migrate CLI; `$DSN` like `postgres://user:pass@host:5432/dbname?sslmode=disable`). The CLI **must be built with the postgres driver tag**, otherwise it fails with `unknown driver postgres`:

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```


```bash
migrate -path db/migrations_postgres -database "$DSN" up
migrate -path db/migrations_postgres -database "$DSN" down 1
migrate -path db/migrations_postgres -database "$DSN" force <version>   # clear dirty state
migrate create -ext sql -dir db/migrations_postgres -seq <name>         # new up/down pair
```

Seed data lives in `db/seeder_postgres/` (`001_ruang.sql`, `002_satuan.sql`, `003_role.sql`, `004_superadmin.sql`) and is applied separately from migrations. Seeders are written to be idempotent (`ON CONFLICT DO NOTHING`), and their conflict target must name the index expression — `ON CONFLICT (lower(kode))`, not `(kode)` — since migration `000009` moved master uniqueness onto `lower(...)`.

Migration `000001` creates only the shared `set_updated_at()` trigger function — every table with an `updated_at` column reuses it.

## Architecture

Layered, one-directional dependency flow. A layer may only import the layers below it:

```
delivery  →  usecase  →  repository  →  PostgreSQL / Redis / upstream HTTP
              ↑ ↑
           model    entity
```

| Package | Role | Rules |
| --- | --- | --- |
| `cmd/web` | HTTP entrypoint | wires config → db/redis → repository → usecase → delivery, then starts Fiber |
| `cmd/worker` | background worker entrypoint | reserved; same wiring, no HTTP |
| `internal/config` | viper, logrus, postgres, redis, fiber constructors | every dependency is built here and injected downward; nothing else reads env directly |
| `internal/entity` | domain structs mapped to DB tables | no JSON tags, no framework imports |
| `internal/model` | request/response DTOs + converters | entity ⇄ model conversion happens here, never in handlers |
| `internal/repository` | data access (PostgreSQL, Redis, upstream HTTP) | all SQL lives here; accepts `context.Context` and a `*sql.DB`/`*sql.Tx` so usecases can compose transactions |
| `internal/usecase` | business logic, validation, transaction boundaries | depends on repository interfaces, returns models — never Fiber types |
| `internal/delivery` | Fiber v3 handlers + routing | parse/bind → call usecase → write response; no business logic, no SQL |

Key consequences:

- **Handlers must not touch `database/sql`.** A query belongs in a repository even if used once.
- **Transactions are owned by the usecase layer.** Repositories take an executor argument rather than opening their own transactions.
- **`internal/entity` never leaks past `internal/usecase`** — the delivery layer only sees `internal/model` types.
- **Captcha sessions** are Redis-only, keyed with a TTL; they are not persisted to PostgreSQL.
- **Wiring happens only in `config.Bootstrap`** (`internal/config/app.go`). No package constructs its own dependencies.

### Errors and responses

There is one response envelope, `model.WebResponse[T]`, and one error path:

- Usecases return `model.Invalid/NotFound/Conflict/Unauthorized/Forbidden(...)` — semantic kinds from `internal/model/errors.go`, **not** HTTP codes and **not** `fiber.NewError`. That is what keeps Fiber out of the usecase layer.
- `statusForKind` in `internal/config/fiber.go` is the single place a kind becomes a status code.
- Handlers just `return err`. The Fiber `ErrorHandler` formats it, unwraps `validator.ValidationErrors` into `validation_errors`, and logs only 5xx.
- A bare `error` (a wrapped SQL failure, say) becomes a 500 with a generic message — internal details never reach the client.
- **Don't hand-roll the driver-error-to-kind mapping.** `internal/usecase/shared.go` holds the six funnels every slice wraps its repository calls in: `notFoundOnNoRows` (`sql.ErrNoRows` → 404), `conflictOnUnique` (`23505` → 409), `invalidOnForeignKey` (`23503` → 400), `conflictOnExclusion` (`23P01` → 409), `invalidOnCheck` (`23514` → 400), and `conflictOnTransisi` (`repository.ErrTransisiStatus` → 409). The SQLSTATE predicates behind them live in `internal/repository/pgerror.go`. `pageMetadata` is there too, and must be called *after* `PageRequest.Normalize` or `total_page` divides by zero.
- `invalidOnCheck` exists mainly for the `kartu_stok` trigger, which raises with `USING ERRCODE = 'check_violation'` for negative stock and for a closed `periode`. A trigger's `RAISE` carries no constraint name, so there is nothing to switch on — each call site supplies its own message, and the database's own text never reaches the client. `periksaPeriode` narrows one of those two causes to a message naming the month, before the trigger has to; it is not a guard, and the combined message stays as the fallback.

Controller boilerplate is fixed — copy it verbatim rather than improvising, because these strings are part of the contract:

- `ctx.Bind().Body(request)` failure → `model.Invalid("malformed request body")`; `ctx.Bind().Query(request)` failure → `model.Invalid("malformed query parameters")`.
- `strconv.ParseInt(ctx.Params("id"), ...)` failure → `model.Invalid("id must be an integer")`.
- Create answers `fiber.StatusCreated`; everything else answers 200 via a bare `ctx.JSON`.
- `WebResponse.Data` deliberately has **no `omitempty`**. An empty slice is "empty" to `encoding/json`, so omitempty would drop the key on exactly the page with no rows and break a client reading `data.length`. The cost is `"data": null` on error responses; keep it.
- Pagination defaults are `PageRequest.Normalize` in `internal/model/model.go`: page 1, size 20, size capped at 100. The usecase calls it; the controller does not.

### Authentication and authorization

Bearer JWT, stateless by decision. Every `/api/v1` route needs a token except `POST /api/v1/auth/login`.

- **Route guards must be the FIRST handler argument.** Fiber v3 registers as `Get(path, handler, handlers...)` and runs the chain in the order given, so `Get(path, controller, guard)` puts the guard *after* the controller — which means never, because a controller does not call `Next()`. The route table looks protected and protects nothing. Write `Get(path, guard, controller)`. `TestRouteGuardsRunBeforeHandler` pins both halves of this, including a subtest that fails if Fiber's ordering ever changes.
- **Tokens cannot be revoked.** Nothing is stored server-side and no lookup happens per request, so there is nothing to invalidate. `is_aktif = false` on a user, or a revoked role, does not reach a token already issued — access ends only at expiry. `jwt.ttl_minutes` (default 60) is the entire bound on that window. Do not "fix" this with a Redis blacklist without revisiting the decision: the blacklist reinstates the per-request lookup that JWT was chosen to avoid. `POST /api/v1/auth/switch-context` (isu #12 fase 4) does not change this: it mints a new token and cannot touch the old one, which stays valid until it expires regardless of what context was switched to. See "Konteks aktif per sesi" below.
- **Grants live in the token claims**, so authorization touches no database. The cost is that a grant made, revoked, or retired takes effect at next login or at the next `switch-context`. Only usable grants are embedded — role `is_aktif`, and if the grant is scoped to a unit, that unit `is_aktif` too — which is where retired-does-not-authorize is enforced; `FindRolesByUserIDs` itself returns retired grants on purpose, for the user-management view. Since isu #12 fase 4, a token also carries **which one grant is currently active**, and authorization checks that alone, not the full list — see "Konteks aktif per sesi" below for what changed and, more to the point, what deliberately did not.
- **`jwt.secret` has no default and the process refuses to start without one** (`config.NewAuthConfig`, minimum 32 characters). A baked-in default is a key every deployment shares, and holding it means minting a `SUPERADMIN` token for any user id. A random per-process key was also rejected: it invalidates every token on restart and breaks outright across more than one instance, both silently.
- **Login answers one message for every failure** — unknown username, wrong password, disabled account. Distinguishing them enumerates valid usernames. The unknown-username path runs a dummy bcrypt compare so it does not return measurably faster than a wrong password, which would leak what the identical message hides.
- `Authenticate` pins the accepted signing method. Without `jwt.WithValidMethods`, the parser trusts the token's own `alg` header and accepts `alg=none`. `TestAlgNoneTokenIsRejected` covers it.
- **The whole authorization policy is one function**, `setupAuthRoute` in `internal/delivery/http/route/route.go`, so it can be read as a whole. Reads are open to any authenticated user; writes split by data owner (`INVENTARIS` for goods/units/rooms/carriers/suppliers, `CASHIER` for customers); `role` and `user` are `SUPERADMIN`-only including reads; every document that writes `kartu_stok` splits by workflow stage instead (`INVENTARIS` types and submits, `SUPERADMIN` posts / rejects / voids) — except `mutasi`, which has no approval stage at all, so the same split lives in the route table alone (`INVENTARIS` reaches `DRAFT`, `SUPERADMIN` posts and voids); `dokumen` has no split at all, because attachments belong to no module and are protected by their state rather than by the caller's role — see "Dokumen and file attachments". **That split is a starting assumption from the three role names, not a spec** — adjust it as the real division of work emerges.
- **`db/seeder_postgres/004_superadmin.sql` is load-bearing.** `POST /api/v1/user` is `SUPERADMIN`-only, so without a seeded first user the API is locked out of itself. It ships `admin` / `admin12345`, a password committed to this repository — treat it as single-use.
- `middleware.SessionFrom(ctx)` is how a handler gets the caller, and it is the only acceptable source for `created_by`/`updated_by` — the id comes from the verified token, never from the request body. `product_controller.go` is the worked example: the controller reads the session and copies `session.UserID` onto the request DTO's `ActorID` field. Every other slice still writes `NULL`.
- Role names are constants in the `route` package (`RoleSuperadmin`, `RoleCashier`, `RoleInventaris`), matching `db/seeder_postgres/003_role.sql`. They are constants precisely because `role.nama` is renameable through the API and the compiler cannot catch a rename — at least the strings to change are in one place.
- Layering holds: the middleware calls `AuthUseCase.Authenticate` and receives a `*model.Session`. The usecase imports `jwt` but never Fiber.

### Pembelian and the posting engine (migration 000012)

The first transaction document, and the first thing that writes `kartu_stok`. Its number generator and its posting path are built to be reused by sales, transfers, usage, and stock counts — build those on this, not on a master slice.

`Create`'s `id_ruang` is validated against the caller's active `unit_kerja` (isu #12 fase 5) — see "`id_ruang` validated against the active unit" below for the shared mechanism. `Get`, `List`, and `Sisa` are scoped by the same active unit on the read side too (fase 6) — see "Read-path scoping" below.

- **The costing arithmetic is pure and lives apart from the I/O.** `internal/usecase/pembelian_alokasi.go` holds `hitungPosting` and `bagiProporsional`, both operating on `*big.Rat` with no database in sight, and `pembelian_alokasi_test.go` is an **internal** test (`package usecase`) — the only one in the repo. That split is deliberate: this is where a mistake is permanent, so it has to be testable without a fixture.
- **Money and quantities are `math/big.Rat`, never `float64`.** 0.1 has no exact binary representation and these figures become a payable and an inventory valuation. `internal/usecase/numeric.go` parses `NUMERIC` text into exact rationals and rounds once, deliberately, at the end. `formatNumeric` uses `big.Rat.FloatString`, which rounds halves away from zero — the same rule PostgreSQL's `ROUND` applies to `NUMERIC`, and they have to agree or a value computed in Go and one the database recomputes differ by a cent nobody can see.
- **`nilai_masuk` is proportional to `qty_diterima_dasar / qty_dasar`, never the full invoice value.** This is the single most expensive thing in the module to get wrong. `kartu_stok` uses a moving average and outgoing rows lock in the cost in force at the time, so 95 of 100 received at full invoice value prices them at 11.052 instead of 10.500 — and any sale before the rest arrives books that permanently into cost of goods sold. Append-only means it can only be reversed, never repaired.
- Cost per base unit is computed against `qty_dasar`, not `qty_diterima_dasar`. Both numerator and denominator scale by the same ratio so the figure is identical, but expressing it against the invoice quantity is what leaves the remaining value available to a follow-up receipt without recomputing anything.
- **Allocation sums exactly.** `bagiProporsional` rounds each share and then pushes the remainder onto the largest basis, earliest one on a tie. Three lines splitting 100 give 33.33 three times, which is 99.99; the missing cent would silently become inventory value nobody was billed for. The same function does freight, the nota discount, the PPN share, and `bagi-rata-koli`.
- Freight basis is `jumlah_koli`, falling back to `qty_diterima_dasar` when every line is still zero — that fallback is the divide-by-zero guard, not a preference. `ditanggung_supplier` zeroes `biaya_angkut` outright.
- **`biaya_angkut` is not part of `total`.** Total is what the supplier is owed; freight is the carrier's bill and reaches the books through `alokasi_biaya` on the lines. Folding it into total would inflate the payable by money owed elsewhere.
- **Two quantity pairs, and they mean different things.** `qty_faktur`/`qty_dasar` is the paper and drives the payable; `qty_diterima`/`qty_diterima_dasar` is the physical count and drives stock. Omitting `qty_diterima` means it equals `qty_faktur`. Over-delivery is rejected in Go *and* by a CHECK. A difference makes `keterangan_selisih` mandatory — a policy, not a constraint.
- **Every state transition takes a row lock first** (`LockByID`, `SELECT ... FOR UPDATE`), then checks the status, then writes with the status repeated in the `WHERE`. Without the lock two concurrent posting requests both read `DIAJUKAN` and both write `kartu_stok`. `KartuStokRepository.HasRef` is the backstop that does not depend on the status column being right.
- `DRAFT → DIAJUKAN → POSTED → BATAL`, with `DIAJUKAN → DRAFT` on rejection. `INVENTARIS` writes and submits; `SUPERADMIN` posts, rejects, and voids. **Every module that writes `kartu_stok` splits its guards by workflow stage rather than by data owner** — `pembelian`, `penerimaan_susulan`, and `retur_pembelian` all carry this shape. See the comment above those routes in `route.go` for why.
- **A cancellation cannot copy the original cost, and that is the schema's decision, not a shortcut.** `kartu_stok_hitung_saldo` overwrites `nilai_keluar` and `harga_pokok_satuan` on every outgoing row with the balance in force at the time, so an application-supplied cost is discarded. Reversals are therefore valued at the current moving average. Undoing the mixing would need a different valuation method; don't try to work around it by writing to those columns.
- **Document numbers come from `document_counter`**, keyed `(prefix, tahun, bulan)`, taken with one `INSERT ... ON CONFLICT DO UPDATE RETURNING` inside the document's transaction. That statement is atomic and holds the row lock until commit, so concurrent requests queue instead of colliding. A rollback leaves a gap — deliberately: gaps are an annoyance, duplicate numbers are a corruption. Reset is monthly and keyed off the **document's** date, so a July invoice typed in August still gets a July number. Add a prefix constant to `repository` rather than typing a literal.
- `no_faktur_supplier` is unique per supplier via a partial `lower(...)` index excluding `BATAL`, so a mistyped nota can be voided and re-entered. Without a purchase order this document is the only trace of the supplier's invoice, and entering it twice raises stock twice.
- `faktor_konversi` is a snapshot from `product_satuan`, resolved for every line in **one** query (`FindFaktorBatch`, `unnest` of two arrays), not one per line. `qty × faktor` must be a whole number because `qty_dasar` is `BIGINT`.
- Lines are replaced wholesale (`PUT .../detail`), never edited one at a time: they are one thing retyped off one piece of paper, and a partial edit leaves the header's totals disagreeing with its own lines between requests. `DeleteDetail` is safe only on a `DRAFT` — a posted line is what `retur_pembelian_detail` points at.
- `status_penerimaan` follows the `status_pembayaran` rule: **always recomputed, never set from a form.** A cache a form can write is a second source of truth.

### Penerimaan susulan (migration 000013)

The second shipment for goods that did not arrive with the first. It adds stock and never a payable — the supplier's invoice was issued in full with the first delivery and booked in full then, so what is still outstanding after that is goods.

`Get` and `List` are scoped by the caller's active `unit_kerja` (isu #12 fase 6), against the parent purchase's room — this module has no `id_ruang` of its own to check on write, since it is copied from the parent, but the read-side scope still needs the room's unit, so `susulanReadColumns` joins it in. See "Read-path scoping" below.

- **Why a separate document rather than raising `qty_diterima`.** A POSTED `pembelian` must not change and `kartu_stok` is append-only, so editing the posted line is not available: it would break document immutability, make cancellation impossible to audit (which `kartu_stok` rows would be reversed?), and erase when the goods actually turned up. The shape that fits is the mirror of `retur_pembelian` — both point at `pembelian_detail`, the goods just move the other way.
- **`harga_pokok_satuan_dasar` is copied from the source line, never recomputed and never read off the current moving average.** That is what makes a purchase and all its follow-ups contribute exactly the invoice value to inventory, and it is why the average does not shift when the remainder turns up. Copying a cost rather than deriving one is the pattern every document deriving from another should follow.
- **Two snapshots from different places, deliberately.** `faktor_konversi` is resolved from `product_satuan` *now*, because the quantity is a fresh count and may arrive in a different unit than the invoice used (five loose pieces short of a line typed in boxes). The cost is copied from the source. Quantity conversion follows current master; cost follows the invoice.
- **The quota check that counts runs at posting, under `PembelianRepository.LockByID`.** The one in `siapkanDetail` only produces a friendlier error sooner. Two drafts may both claim the same remainder — a draft is not a delivery — and the second must fail when it tries to consume what the first already took. `TestSisaDiperiksaUlangSaatPosting` pins exactly that.
- Only **POSTED** follow-ups count towards the outstanding figure. `pembelianDetailSusulan` in `pembelian_repository.go` is that predicate, declared once and reused by `FindDetail`, `FindDetailByID`, and `RecalculateStatusPenerimaan` — three copies of it would drift.
- **`pembelian.status_penerimaan` is rewritten from here**, by `RecalculateStatusPenerimaan`, after posting and after voiding. The purchase is POSTED by then and can no longer recompute its own cache, so this is the only thing keeping it honest.
- **Three derived quantities on a purchase line, and they answer different questions.** `selisih_dasar` is what the first delivery was short and is frozen once posted; `qty_susulan_dasar` is what turned up later; `sisa_dasar` is what is still owed. Don't collapse them.
- `penerimaan_susulan_detail_baris_uidx` stops one source line appearing twice in one document. Without it two lines for the same source each pass the quota check on their own and together exceed it. The usecase catches it first so the message names the field.
- **Cancelling a purchase is refused while a POSTED follow-up exists** (`HasPostedSusulan` → 409). The purchase's cancellation only reverses rows it wrote itself, so the follow-up's stock would survive with nothing explaining it — and it could not be reversed afterwards either, since its own cancellation path needs a purchase that is still POSTED. Void the follow-up first.
- `jenis_transaksi` gained `'PENERIMAAN_SUSULAN'`. **`ALTER TYPE ... ADD VALUE` cannot be undone** — PostgreSQL has no `DROP VALUE`, and removing one means rebuilding the type and every column using it, including append-only `kartu_stok`. The down migration says so plainly and leaves the value behind rather than pretending. `ADD VALUE IF NOT EXISTS` keeps the up idempotent after a down. It is safe inside golang-migrate's transaction on PG 12+ as long as the value is not *used* in the same transaction, which this migration does not do.
- No freight columns. Value entering stock is the remaining share of the original invoice value, so one invoice contributes exactly its own value however many times the goods turn up. If a follow-up shipment ever needs its own carrier bill, that is a schema change and a deliberate one — it makes the second batch cost more per unit than the first.
- `id_supplier` and `id_ruang` are copied from the purchase, not chosen. Goods that need to move rooms after arrival are a `mutasi`.

### Retur pembelian (migration 000014)

Goods sent back to the supplier — isu #4 fase 5. Deliberately the mirror of `penerimaan_susulan`: same parent, same copied cost, same quota-on-a-line shape, goods moving the other way. Read the two side by side; what follows is only what the direction changes.

`Get` and `List` are scoped the same way `penerimaan_susulan`'s are (isu #12 fase 6), against the parent purchase's room. See "Read-path scoping" below.

- **The tables already existed** (migration `000005`); `000014` adds the approval flow, the cancellation columns, the status CHECK, a `(tanggal DESC, id DESC)` index, and the per-document unique index on the source line. **No `ALTER TYPE`** — `'RETUR_PEMBELIAN'` has been in `jenis_transaksi` since `000002`, which is lucky, because `ADD VALUE` cannot be undone.
- **What may be returned is what actually arrived**, `qty_diterima_dasar + Σ POSTED susulan − Σ POSTED retur`. Note which quantity is absent: `qty_dasar`. The invoice quantity is what the supplier billed, and goods that never turned up cannot be sent back — a shortfall is chased with a `penerimaan_susulan`, not returned. `pembelianDetailRetur` in `pembelian_repository.go` is that sum, declared once next to `pembelianDetailSusulan`.
- **This is a different axis from the outstanding quantity, and mixing them is the mistake to avoid.** Goods returned were still received, so `status_penerimaan` is **never** recomputed from here — a return does not make a delivery incomplete and does not entitle anyone to a follow-up shipment. `sisa_dasar` and `qty_dapat_diretur` can both be nonzero on the same line. That absence is the one asymmetry with `penerimaan_susulan` worth checking for on sight, since copying that module wholesale would wrongly add the call.
- **`total` and what `kartu_stok` records leaving are different numbers, on purpose.** `total` is the invoice cost, summed from `harga_pokok_satuan_dasar` copied off the source lines — that copy is what makes a purchase and its return cancel out, and migration `000005` says so. `kartu_stok` values every outgoing row at the moving average in force at the time, because those goods were blended into older stock the moment they arrived. Neither figure can be made to be the other.
- **A return reduces the payable, but never by its own `total`.** `total` is derived from cost, which includes the freight share and whatever PPN treatment applied, while `pembelian.total` excludes freight entirely — subtracting one from the other over-credits the supplier by money that went to the carrier. The payable uses `nilai_kredit_utang` instead, a separate column frozen at posting; see "Pembayaran utang and the payable side". Posting and voiding a return both call `PembelianRepository.RecalculateStatusPembayaran`.
- **Cancelling a purchase is refused while a POSTED return exists** (`HasPostedRetur` → 409), and the reason compounds. The purchase's cancellation reverses the full received quantity while the return has already taken part of it out, so the reversal drives the balance negative and is refused by the trigger with a message about stock rather than about the return. Even where the balance allows it, the return would be left pointing at a `BATAL` purchase whose reversal already accounted for those goods — the same shortfall twice.
- **Voiding a `penerimaan_susulan` whose goods have been returned is refused by the trigger**, not by a check in Go, and the trigger is the right arbiter: the balance is computed inside it under an advisory lock precisely so no reader can decide it first. It surfaces as `invalidOnCheck` → 400. `TestBatalSusulanDitolakSaatBarangnyaSudahDiretur` pins it.
- **The negative-stock guard on this module's own posting is defensive, not currently reachable.** What a line may send back is exactly what it brought in, so the quota can never exceed the room's stock while purchase, follow-up receipt, and return are the only documents moving it. That stops holding the moment `penjualan`, `mutasi`, or `pemakaian` exist, which is why `invalidOnCheck` is wired now rather than discovered then.
- `alasan` is nullable in the schema but **required by the usecase**, and a patch may not clear it. It is the only record of why goods already paid for went back. Following the `keterangan_selisih` precedent: a policy, not a constraint.
- Cancellation appends reversing rows with `NilaiMasuk = asal.NilaiKeluar` — what the ledger recorded leaving is the one figure that makes the pair sum to zero. An outgoing row's own `NilaiMasuk` is sent as an explicit `"0"`, since the column is NOT NULL and an empty string is not a NUMERIC.
- `qty_retur_dasar` and `qty_dapat_diretur` ride on `PembelianDetailResponse`, so the return-entry screen needs no endpoint of its own. Adding them changed `pembelianDetailReadColumns` and therefore the scan order in both `FindDetail` and `FindDetailByID`.
- Prefix is `RB`; the series is its own, independent of `BL` and `PS`.

### Pembayaran utang and the payable side (migration 000015)

Money paid to suppliers — isu #4 fase 6, the last phase. The tables came from migration `000008`; `000015` adds the value guards, the giro CHECKs, the per-document unique index on the invoice, and `retur_pembelian.nilai_kredit_utang`.

- **It is the only transaction module that touches no stock, and that absence shapes everything.** `DRAFT → POSTED → BATAL`, with **no `DIAJUKAN`** — do not add one to match `pembelian`. The approval stage exists there because `kartu_stok` is append-only: a wrong posting can only be reversed, and the reversal is valued at a moving average that has since shifted. An allocation has no such residue; it can be voided and every cache recomputed exactly. The two-person control survives as the route split (`CASHIER` prepares, `SUPERADMIN` releases the money), one state fewer.
- **Three rules, and they are deliberately not symmetric.** A payment may be allocated at most up to its own amount, and **less is normal** — the remainder is a credit sitting with the supplier, which is why `alokasi` may be empty on create. An invoice may receive at most what it still owes. Never force allocation to balance.
- **An uncashed giro is not a payment**, and this is the trap in the module. Posting a `BELUM_CAIR` giro freezes its allocations and closes the document while leaving every payable exactly where it was; `Cairkan` is what moves `status_pembayaran`. The consequence is that the remaining-balance check **runs again at clearing**, not just at posting — a cash payment can settle the same invoice in between. `TolakGiro` needs to give nothing back, because nothing was ever taken.
- **`nilai_kredit_utang` is not `retur_pembelian.total`.** Cost carries the freight share paid to the carrier, which the supplier never received. The credit is `pembelian.total × nilai_faktur_retur / pembelian.subtotal`, where `nilai_faktur_retur` sums `subtotal / qty_dasar × qty_retur_dasar` over the source lines. **Scaled against `total` rather than taken raw from the line values**, because `total` already carries the nota discount, the PPN, and the rounding line — crediting raw line values for a full return over-credits by exactly the nota discount, money that reduced the bill in the first place. The scaling is what makes the invariant checkable: a purchase's returns never credit more than its `total`, and credit exactly `total` when everything goes back.
- **The credit is frozen at posting, not computed on read.** It derives from lines of two documents that are both POSTED and can no longer change, so recomputing would always give the same answer — until one day it does not, and an old payable silently changes value. Every money figure in this project is a snapshot.
- **`status_pembayaran` is a cache, same rule as `status_penerimaan`:** always recomputed, never set from a form. `RecalculateStatusPembayaran` is one statement so there is no window where the cache disagrees with the rows it summarises, and **everything that can change the answer calls it** — posting and voiding a payment, clearing and rejecting a giro, and posting and voiding a return. Two SQL fragments back it, declared once in `pembelian_repository.go`: `pembelianAlokasiEfektif` (POSTED payments, and for giro only `CAIR` ones) and `pembelianKreditRetur`.
- `SEBAGIAN` covers a return-only credit with no money paid at all. That is correct — the invoice genuinely is partly settled — and both figures ride on the response so a screen can say which one did it.
- **Cancelling a purchase is refused once it has been paid**, including while an uncashed giro points at it. An uncashed giro has not reduced the payable, but it is a document circulating against that invoice.
- `pembayaran_utang_alokasi_baris_uidx` stops one invoice appearing twice in one payment — the same trap as `penerimaan_susulan_detail_baris_uidx` and `retur_pembelian_detail_baris_uidx`, and the usecase catches it first so the message names the field.
- `id_supplier` and `metode` are **absent from the update DTO**. The first is who the money goes to, and changing it would leave every allocation pointing at another supplier's invoices; the second decides whether the giro columns may be filled at all. Change either by cancelling and re-entering — which is also what the bank statement will show.
- `GET /supplier/{id}/utang` is a **read that is not a module**, following `riwayat_beli`: the query lives in `pembelian_repository.go`, `SupplierUseCase` borrows `PembelianRepository`, no migration. It is ordered **oldest first**, unlike every other list in this API, because it is a queue to work through.
- Prefix is `PU`; the series is its own.

### Dokumen and file attachments (migration 000016)

Isu #5. Uploaded files — photographed invoices, damaged-goods photos, signed delivery notes — attached to whichever document they belong to. Two things make it unlike every other slice: it holds a store that is not the database, and its reference is polymorphic.

- **Upload first, attach later, and that is forced by the physical world.** The photo is taken while the box is being opened, before the `pembelian` exists. So a row is born with `ref_table` and `ref_id` NULL, and `POST /dokumen/{id}/tempel` claims it afterwards. **The nullable `ref_id` is the feature**: it is what makes an orphan possible and, through a partial index `WHERE ref_id IS NULL`, what makes one cheap to find.
- **Attaching is one endpoint here, not a `dokumen_ids` field on every document.** A module that starts accepting attachments adds one line to `repository.RefTableDokumen` — no migration, no DTO change, no second copy of the rules. That map is also the only thing standing between `ref_table` and an arbitrary string: there is no foreign key behind a polymorphic reference, and it is why `StatusRef` may interpolate the table name into SQL at all — what reaches the query is a key of the map, never the caller's string.
- **The write order is the one thing that cannot be swapped.** Upload writes the file, then the row, and deletes the file if the row fails. Deletion goes the other way: file first, then the row's `deleted_at`. Both follow the same rule — whichever half survives a crash has to be the recoverable one. A row pointing at a missing file cannot be repaired by anyone, because nothing knows what the file should have contained; a file whose row was never written is caught by the caller, and a row still marked live is simply retried.
- **MIME comes from the bytes, extension comes from the MIME, and the storage name comes from a UUID.** The client's filename is display text and reaches the filesystem never — `nama_asli` may be `../../config.json` and mean nothing. `LocalDokumenStorage.path` **rejects** rather than sanitises: `filepath.Base` would quietly turn a traversal into a write to the wrong place, and a bug that makes no sound is worse than an error.
- **The size limit is enforced on the stream** (`io.LimitReader`, reading one byte past the limit so "exactly at" is distinguishable from "truncated"). `Config.BodyLimit` is derived from `dokumen.max_size_mb` in `config.NewFiber` — Fiber's default is 4 MB and fasthttp refuses an oversized body before any handler runs, so leaving it alone silently caps a configured 10 MB at 4.
- **Downloads stay behind the token, and always as an attachment.** `Content-Disposition: attachment` plus `X-Content-Type-Options: nosniff`, so a stored HTML or SVG cannot execute in the application's origin. `dispositionLampiran` reduces the quoted form to safe ASCII and carries the real name in RFC 5987 `filename*` — `nama_asli` is arbitrary client bytes, and a CR in a header value is header injection.
- **The cleanup job works from rows, never from a directory scan.** A scan would sweep up files whose row is written but not yet committed — the seconds between `Storage.Tulis` and `tx.Commit`. It is safe against a second worker in two layers: a session-level `pg_advisory_lock` (on a `*sql.Conn`, since releasing it from another pooled connection does nothing), and one transaction with a row lock per file, so an attach racing a sweep blocks instead of losing its bytes.
- **Soft delete, and it is the only `DELETE` in the API.** The row survives with `deleted_at`; only the file goes. That trace is also what makes the sweep re-runnable without remembering what it already did.
- Removal is allowed **only while orphaned or while the parent is `DRAFT`**. Attaching to a `BATAL` parent is refused for the mirror reason: it could never be removed again.
- `dokumen` carries **no role guard beyond being authenticated**, and that is not the reads-are-open rule stretched over writes. Attachments belong to no module, so no data owner can be named; what protects them is state — inert until claimed, refused past `BATAL` or ten files, unremovable past `DRAFT`.
- Config lives under `dokumen.*` and is read by **both** entrypoints. `dokumen.storage_path` must resolve to the same directory in `web` and `worker`, which in compose is one volume mounted on both — point them apart and the sweep finds rows, finds no files, and marks them deleted anyway.
- `checksum_sha256` reports `duplikat_dari_id` and never refuses: one scan legitimately belongs to two documents.

### Periode and book closing (migration 000017)

Isu #6. The act that makes a month refuse further stock movements. The tables and the trigger's respect for them predate this by fourteen migrations; what was missing was any Go at all, so every month stayed open forever.

- **It is master-data shaped but cross-cutting.** Follow `supplier`, not `pembelian` — no number, no lines, no posting. What makes it unlike a master slice is that the row it writes is read by the `kartu_stok` trigger on every insert, so **every module that writes stock inherits the refusal without a line of code**: sales, `mutasi`, `pemakaian`, and stock opname will get it for free. That is also why the write guard is `SUPERADMIN` rather than a data owner — closing a month is not this module's data changing, it is every other module losing the ability to post into it.
- **A month with no row is open**, which is migration `000004`'s decision, and it shapes everything: closing a month **creates** its row (hence the upsert), `Get` answers a synthetic `BUKA` rather than 404, and `Search` cannot list months nobody has touched — the table records closings, not a calendar.
- **Routes are keyed `(tahun, bulan)`, not `/{id}`**, the only module that departs from that pattern. The pair is the real identity, `periode_tahun_bulan_uidx` says so, and an id-keyed route could not address the ordinary case at all since an unclosed month has no id. The response carries **no `id` field either**, so a stored month and a synthetic one have the same shape.
- **The reversing-row date is the decision this issue was really about.** Posting is dated on the document; cancellation is dated `time.Now()`. So **voiding a document whose period has since closed still works** — the reversal lands in the current period and the closed month's figures do not move. That is the ordinary accounting treatment, and the alternative leaves a mistyped document from a closed month with no way out. The cost is stated plainly in `PembelianUseCase.Batal` and pinned by `TestBatalDokumenPeriodeTutupMasukPeriodeBerjalan`: the document reads `BATAL` while the closed month's ledger still carries its movement, so **anything reporting per period must read `kartu_stok`, never the document status**. What *can* block a cancellation is the **current** period being closed.
- **Closing and posting are serialised by an advisory lock, not by a row lock.** The trigger takes `pg_advisory_xact_lock_shared` on `hash('periode:' || tahun || '-' || bulan)` before reading the status; `PeriodeRepository.Lock` takes the exclusive side. `SELECT ... FOR SHARE` on the row was rejected because an unclosed month **has no row** — precisely the case that matters, since closing is what creates it. The key expression is duplicated between migration `000017` and `periodeLockKey`, and the two must produce the same string or neither side waits for the other. `TestTutupMenungguPostingYangSedangBerjalan` fails against the pre-`000017` trigger, so it is a real test rather than a decorative one.
- The lock lives in the same `hashtextextended(..., 0)` key space as the `(barang, ruang)` lock, separated only by the `'periode:'` prefix. A 64-bit collision costs an unrelated writer a short wait, never a wrong answer. The periode lock is taken **first**, uniformly, so there is no path to a deadlock.
- **Reopening is allowed, `SUPERADMIN` only**, and `000017` adds `dibuka_oleh`/`ts_buka` for it. Without them, closing after a reopening overwrites `ditutup_oleh`/`ts_tutup` and nothing records that the month was ever reopened. `Tutup` therefore leaves the reopening columns alone — a closing that cleared them would erase the only thing they exist for. A pair of columns rather than an audit table: full history of every closing is a different question, and its table can be added when it is actually asked.
- **`Buka` is an UPDATE, not an upsert, and the asymmetry with `Tutup` is the point.** Reopening a month with no row would insert a row saying `BUKA`, which is what a missing row already means. It repeats `status = 'TUTUP'` in the `WHERE`, so `sql.ErrNoRows` covers both "never closed" and "someone reopened it first" — one message fits both, and it is a 409. Closing an already-closed month is a 409 too: neither changes anything, and a 200 would let a caller believe otherwise.
- **Closing is not required to be sequential.** August may be closed while July is open. Requiring an order would force closing every unused month first, and nothing can break from the gap — enforcement is per month inside the trigger, not a running total.
- **`periksaPeriode` in `shared.go` is for the message, not the guard**, exactly like `ExistsByKode` against a unique index. A trigger's `RAISE` carries no constraint name, so `invalidOnCheck` cannot separate a closed period from insufficient stock and every call site had to say "either". The pre-check runs in `Posting` (on the document's date) and in `Batal` (on **today's**, since that is what the reversal is dated) across all three stock-writing modules, and answers 400 to match what the trigger's rejection maps to. A closing that commits between the check and the insert is still caught by the trigger, just with the vaguer message.
- No `created_at`/`updated_at` and no `set_updated_at()` trigger. `ts_tutup` and `ts_buka` already answer the useful question, and nothing else about the row changes.
- `truncateMaster` deletes `periode` before `users` (it references both actor columns). Getting this wrong does not fail a later test's insert — it silently refuses its posting.

### Mutasi antar ruang (migration 000018)

Isu #7. Goods moving from one `ruang` to another — the fourth document to write `kartu_stok` and the first to write **in both directions at once**. The tables came from migration `000007` and `'MUTASI_KELUAR'`/`'MUTASI_MASUK'` have been in `jenis_transaksi` since `000002`, so **no `ALTER TYPE`**; `000018` adds the status CHECK, the cancellation columns, `mutasi_status_idx`, and a `(tanggal DESC, id DESC)` index.

`Create` and `Update` validate `id_ruang_asal` — never `id_ruang_tujuan` — against the caller's active `unit_kerja` (isu #12 fase 5): cross-unit transfers stay allowed, only the room goods are said to be leaving is checked. See "`id_ruang` validated against the active unit" below. `Get` and `List` inherit the identical source-only asymmetry on the read side (fase 6) — see "Read-path scoping" below.

- **One `mutasi_detail` line is two `kartu_stok` rows, in one transaction.** That is migration `000007`'s own description of the table. Splitting it into two documents would let goods leave the warehouse without ever entering the shop, with nothing holding the halves together. Goods appearing with no origin are not a mutasi at all — that is `stok_opname`, and `SO_SURPLUS`/`SO_DEFISIT` are waiting for it.
- **The incoming row is valued at exactly `nilai_keluar` from the outgoing row, read back from `RETURNING`.** This is the module's whole correctness argument and the most expensive thing to get wrong. Migration `000007` states the rule — cost follows the source room or moving goods changes the value of inventory — but the application *cannot compute that cost*: the source room's moving average is known only to `kartu_stok_hitung_saldo`, which reads it inside the advisory lock, and the trigger overwrites `nilai_keluar` and `harga_pokok_satuan` on every outgoing row anyway. So the order of the two inserts is forced, and the constraint is what makes it right. `ReturPembelianUseCase.Batal` fills `NilaiMasuk` from a `NilaiKeluar` for the same reason.
- **`mutasi_detail.harga_pokok_satuan_dasar` is written at posting, not at draft**, from what the outgoing row reported. The column is nullable precisely for that. A cost typed on a draft is the average at draft time, which is a different number and wrong in a way nobody would notice.
- **No `DIAJUKAN`, and the reason differs from `pembayaran_utang`'s.** `DRAFT → POSTED → BATAL`, seven endpoints instead of nine. The rule for the other three stock writers is that an append-only ledger makes a wrong posting unrepairable, so it buys an approval stage. Mutasi's mistake is far cheaper: goods recorded in the wrong room, while **total stock and total inventory value do not move at all** — no outside party, no money, and the correction is another mutasi the same person may already write. `pembayaran_utang` drops the stage because voiding leaves no residue; mutasi's cancellation *does* leave residue. The justification is the size of the bet, not the tidiness of the undo.
- **Dropping `DIAJUKAN` moves the entire two-person control into the route table**, which is why that split matters more here than it looks: `INVENTARIS` reaches `DRAFT`, `SUPERADMIN` posts and voids. The cost is that there is no "this draft is ready" signal — `DRAFT` means both "still being typed" and "please post it" — so the list endpoint **must** be able to filter `status=DRAFT`, and `terlama_dulu` orders it oldest first like `GET /supplier/{id}/utang`, because a queue is read to be worked through. If that turns out not to be enough, add `DIAJUKAN`; adding a value to `mutasi_status_check` is far cheaper than removing one would have been.
- **Two advisory locks in one transaction, which is a real ABBA.** The trigger locks per `(id_barang, id_ruang)` and a mutasi takes two for the same product, ordered by which way the goods go — so two opposite transfers deadlock. Impossible before this module, since every other document touched one room. `KartuStokRepository.KunciSaldo` takes them all up front in canonical order; the alternative (map `40P01` to a 409 and ask the client to retry) hands a real defect to whoever is at the counter. `TestMutasiBerlawananArahTidakDeadlock` fails with `deadlock detected` on every run without it, so it is a real test rather than a decorative one.
- **The periode lock is taken first, uniformly**, because that is the order the trigger takes them on every insert. A writer that pre-locks balances without it opens a different cycle: a book closing queued for the exclusive periode lock, a posting holding it shared and waiting on our balance lock, and us queued behind the closing. `PeriodeRepository.LockShared` exists only for that.
- **Cancellation is not symmetric in value, and cannot be made so.** The row leaving the destination room is valued at that room's current moving average, which may have shifted. So a transfer and its void always cancel in quantity and not always in value — the same limitation already recorded for `pembelian`. It can also be **refused outright**: if the goods have since left the destination room, the reversing row drives that balance negative and the trigger says no. That is correct, and the remedy is another mutasi.
- **The same product may appear on two lines**, unlike `penerimaan_susulan`, `retur_pembelian`, and `pembayaran_utang`, which each forbid it with a unique index. There the quota is held on a parent line and two rows each pass alone; here the quota is the source room's balance, the usecase sums lines per product before checking, and the trigger checks every insert. Two lines in different input units are a legitimate way to type a transfer.
- **Both rooms may change while `DRAFT`**, unlike `pembayaran_utang`, which keeps `id_supplier` out of its update DTO. No `mutasi_detail` row names a room, so nothing is left pointing at the wrong place. The `id_ruang_asal <> id_ruang_tujuan` check is checked against the **stored** row, not the patch: moving only one of the two can collide with the other one already there.
- `periksaSaldo` is for the message, not the guard — same relationship `periksaPeriode` has to a closed period. It runs *after* `KunciSaldo`, which is what makes the figure it reports actually true for the rest of the transaction rather than merely friendlier.
- No money column anywhere: no subtotal, discount, PPN, freight, or koli. `pembelian_alokasi.go` has nothing to offer and `math/big.Rat` is needed only to convert quantities. Prefix is `MT`.

### Unit kerja and location-bound authority (migration 000019, isu #12)

Isu #12 proposes answering not just *who* but *acting as what, and where* — a
work-context system where `user_role` grants a role at a specific `unit_kerja`,
and a session carries exactly one grant as its active context. The full
proposal is six phases, and **this codebase has all five that were
scoped**: the decisions, the `unit_kerja` master and `ruang.id_unit_kerja`
(phase 2), `user_role.id_unit_kerja` itself with its `NULL`-safe `ReplaceRoles`
diff and the `grants` DTO (phase 3, migration `000020`), active session
context via `POST /api/v1/auth/switch-context` (phase 4, no migration — see
"Konteks aktif per sesi" below), and `id_ruang` validated against the active
unit on write paths (phase 5, no migration — see "`id_ruang` validated
against the active unit" below). Phase 6 — read-path scoping and a
role-as-snapshot column on documents — was explicitly deferred by the issue
itself, not overlooked; see that section for why.

Three decisions the issue asked to make now, even before the phases that need
them are built, because two of them touch tables every stock-writing module
already depends on and the price of deciding late is a breaking change to
numbers already printed on paper:

- **`document_counter` gets a per-unit series.** The key becomes `(prefix,
  id_unit_kerja, tahun, bulan)` rather than `(prefix, tahun, bulan)`, so a
  document number is traceable to the outlet that issued it. **Not
  implemented yet** — `DocumentCounterRepository.Next` still keys on the old
  triple, and every transaction module still reserves numbers the old way.
  Change the key before any two real outlets start sharing one series: once a
  number is on paper in a supplier's hands, the key cannot change under it.
- **`periode` stays global**, not per `unit_kerja`. One close/open per
  `(tahun, bulan)` for the whole company. The alternative — an outlet closing
  August while another is still posting into it — produces a consolidated
  report that cannot be explained, and nothing in this codebase asks for that.
  If a real need for per-unit closing shows up later, `periode` gains
  `id_unit_kerja` and the `kartu_stok` trigger's advisory-lock key
  (`'periode:' || tahun || '-' || bulan`, duplicated between migration
  `000017` and `periodeLockKey`) has to grow the unit into that same
  expression on both sides, or one side stops waiting for the other.
- **Cross-unit `mutasi` is allowed.** A transfer's `id_ruang_asal` and
  `id_ruang_tujuan` may belong to different `unit_kerja` — moving stock
  between outlets is exactly what `mutasi` is for, and restricting it to one
  unit would leave no document for the ordinary case of restocking a branch
  from the central warehouse. Nothing about `mutasi`'s posting changes: it
  stays partitioned by `(id_barang, id_ruang)`, never by unit.

**This is not `users.role_active` revived.** "Users and roles" below says
plainly not to reintroduce that column, and the reason still holds: it was
`UNIQUE` across the whole table rather than per user, and its FK pointed at
`user_role (id)` without `user_id`. Phase 3 does add a column to `user_role`
(`id_unit_kerja`), but nothing on `users` itself, and the mechanism is entirely
different: a grant is a row a caller *holds*, not a pointer to "the" active
one — a user can hold the same role at two units simultaneously as two rows,
which `role_active` structurally could not express even before its bug. Phase
4's active-context claim (see "Konteks aktif per sesi" below) confirms the
shape: it lives in the JWT, not in a column on `users`, and a session's active
grant is one row of `user_role` copied into the token — never a pointer stored
server-side.

**`unit_kerja` is a plain master slice — follow `supplier`.** Nullable
case-insensitive unique `kode` (`unit_kerja_kode_lower_uidx`), PATCH presence
semantics via `Optional[T]`, retirement with `is_aktif = false`, no `DELETE`.
It carries no telephone/address/NPWP — just `kode`, `nama`, `is_aktif`, and the
usual audit columns — so its shape is closer to `satuan`, but it follows
`supplier`'s `ExistsByKode` (optional, checked only when supplied) rather than
`satuan`'s `ExistsByNama` (required, unique). `nama` is **not** unique.

**`ruang.id_unit_kerja` is `NOT NULL`, and the backfill lives in the migration,
not the seeder.** A `ruang` with no unit is a `ruang` nobody can decide is
theirs to use, so the column is required from the start rather than eased in.
The migration itself creates one default unit (`kode = 'PUSAT'`, `nama = 'Unit
Utama'`) and points every existing `ruang` row at it inside the same
transaction, *before* adding the `NOT NULL` — that is what makes the migration
safe on a database that already has `ruang` rows, not only on a fresh one.
Relying on a seeder for the backfill would only ever have been safe for a
fresh database, since a seeder is never guaranteed to run before a later
migration's `NOT NULL` lands on a database upgrading in place. `001_ruang.sql`
was updated to point its five rows at `'PUSAT'` by lookup, not by a hardcoded
id, since the migration makes no promise about which id that default row gets.

`RuangUseCase.Create` validates `id_unit_kerja` names an active `unit_kerja`
before writing — the same reasoning as `RoleRepository.CountActiveByIDs` for
role grants: the foreign key alone cannot tell a retired unit from a live one,
and its message names a constraint instead of the field. `ruang` still has no
`PATCH`, so a room's unit cannot be changed through the API — that was already
true before this issue and phase 2 did not need to add it.

### Wewenang bertempat: `user_role.id_unit_kerja` (migration 000020, isu #12 fase 3)

A grant is now the pair `(role, unit_kerja)`, not just a role. `NULL` means "every
unit" — the shape the seeded `SUPERADMIN` grant takes, and the shape every grant
made before this migration keeps, since the column was added with no default.
`user` is the module that owns this: granting and revoking happen while editing
a user (`ReplaceRoles`), never while editing a role, same as before this phase.

**Two unique indexes, not one, because a unique index does not constrain
`NULL`.** `user_role_grant_uidx` on `(user_id, role_id, id_unit_kerja)` covers
every scoped grant, but PostgreSQL treats `NULL <> NULL`, so that index alone
would let ten identical *global* grants for the same `(user, role)` pair sit in
the table at once. `user_role_grant_global_uidx` — a partial index
`(user_id, role_id) WHERE id_unit_kerja IS NULL` — is what actually closes that
gap. Both are required; dropping either one reopens a different hole.

**The same role may now be held at more than one unit, as two distinct rows.**
"INVENTARIS at outlet A" and "INVENTARIS at outlet B" are not a duplicate to
collapse — they are two grants, and both indexes above are built around that
being legal. Everywhere a single row used to mean "this user has this role", it
now means "this user has this role, possibly several times, each at a
different place or nowhere in particular."

**`ReplaceRoles`'s diff had to become `NULL`-safe, and the old code could not
have been patched in place.** The delete used to be `role_id <> ALL($2)` — that
comparison is `NULL` (not `TRUE`) whenever either side is `NULL`, so a plain
port to `(role_id, id_unit_kerja) <> ALL(...)` would silently keep every global
grant no matter what the replacement set said, because a `NULL` id_unit_kerja
can never prove itself `<>` anything. The fix is `NOT EXISTS (... WHERE
t.role_id = ur.role_id AND t.id_unit_kerja IS NOT DISTINCT FROM
ur.id_unit_kerja)` — `IS NOT DISTINCT FROM` is the one comparison PostgreSQL
defines to treat two `NULL`s as equal. `TestUserRevokingGlobalGrantActuallyDeletesIt`
pins exactly this: revoking a user's only (global) grant down to `[]` must
leave zero `user_role` rows, not one nobody can prove should be gone.

**The insert is two statements, not one, for the same underlying reason.** A
single `INSERT ... ON CONFLICT` names exactly one arbiter index, and a scoped
grant and a global grant are protected by two different ones. `ReplaceRoles`
therefore filters its input in Go — rows with a non-nil `IDUnitKerja` go
through `ON CONFLICT (user_id, role_id, id_unit_kerja) DO NOTHING`, rows with a
nil one go through `ON CONFLICT (user_id, role_id) WHERE id_unit_kerja IS NULL
DO NOTHING` — rather than trying to make one statement's arbiter clause satisfy
both indexes at once, which PostgreSQL has no syntax for.

**The DTO changed shape, not just name.** `role_ids` (`Optional[[]int64]`)
became `grants` (`Optional[[]model.GrantRequest]`), where `GrantRequest` is
`{id_role, id_unit_kerja}` and `id_unit_kerja` is an optional, nullable
pointer. Keeping the old key name on a payload that no longer holds bare ids
would have been the misleading choice. The three-state presence semantics are
unchanged: absent leaves grants alone, `[]` revokes everything, a list
replaces the whole set — and deduplication is now by the `(id_role,
id_unit_kerja)` pair (`usecase.toGrants`), not by id alone, so
`[{id_role:1},{id_role:1,id_unit_kerja:2}]` is two grants, not one collapsed
into a conflict. `model.Optional[[]model.GrantRequest]` is registered in
`config.NewValidator` alongside the other `Optional` instantiations, or its
`dive` tag would silently stop validating each `GrantRequest`'s own fields —
the mechanism `TestValidatorDivesIntoOptionalSlice` already pins for
`Optional[[]int64]` extends here without changes to the validator itself,
because `dive` descends into struct fields automatically once
`WithRequiredStructEnabled()` is set.

**`id_unit_kerja` is validated active independently of `id_role`, with its own
message.** `UserUseCase.requireActiveGrants` runs `RoleRepository.CountActiveByIDs`
against the distinct role ids and, only if any grant names a unit,
`UnitKerjaRepository.CountActiveByIDs` against the distinct unit ids — two
separate counts because a bad role and a bad unit are different problems, and
collapsing them into one message would leave an operator guessing which field
to fix. Both are pre-checks only; `invalidOnForeignKey` remains the backstop
for the race where a role or unit is retired between the check and the write.

**`FindRolesByUserIDs` still costs one query for a whole page**, `LEFT JOIN
unit_kerja` added alongside the existing `JOIN role`. A grant whose unit was
retired after being granted is still returned — same rule as a retired role —
because the grant is still real and still needs revoking; `RoleRef.IsAktif`
already told the role story, and `RoleRef.IsAktifUnitKerja` is its counterpart
for the unit, added in phase 4 alongside `IDUserRole` once the grant's own row
identity and its unit's liveness both turned out to matter beyond just this
view — see "Konteks aktif per sesi" below for what needs them.

**`db/seeder_postgres/004_superadmin.sql`'s `ON CONFLICT` had to change target.**
It used to name `(user_id, role_id)`, matching the bare index this migration
drops. It now names `(user_id, role_id) WHERE id_unit_kerja IS NULL` — the
partial index — since the seeded grant is, and must stay, global.

Phase 3 ends here; phase 4 (active session context) is its own section below,
and phase 5 (`id_ruang` validated against the active unit on write paths)
remains future work under the same issue.

### Konteks aktif per sesi: `switch-context` (isu #12 fase 4, no migration)

Phase 3 made a grant `(role, unit_kerja)`; phase 4 is what makes a **session**
answer "acting as which one, right now" — a token now authorizes as **one**
active grant rather than the union of everything the user holds. No schema
change: this phase lives entirely in the JWT claims and the two usecases that
mint them, `Login` and the new `SwitchContext`.

- **`model.Grant` and `model.ActiveContext` are the one pair of types for
  three jobs.** They are the JWT claim shape, `Session`'s own fields, and what
  `LoginResponse`/`SessionResponse` hand a client — the same struct, tagged
  once, rather than three shapes kept in sync by hand. `claims.Grants
  []model.Grant` and `claims.Aktif *model.ActiveContext` decode straight into
  `Session.Grants`/`Session.Aktif` with no intermediate conversion; only
  `entity.RoleGrant` (the database-shaped version, keyed by `Role Role` and
  carrying `IsAktifUnitKerja`) needs `usecase.toGrantList`/`toActiveContext` to
  cross into it.
- **`Session.HasRole` compares the active grant alone, never the full list.**
  `s.Aktif != nil && strings.EqualFold(s.Aktif.Role, name)` — that one-line
  change is the entire enforcement mechanism. `RequireRole` and every guard in
  `route.go` are **byte-for-byte unchanged**; a session with `Aktif == nil`
  fails every `RequireRole` check by construction, because `HasRole` has
  nothing to compare against. That is the measure the issue itself proposed
  for whether the design fit, and it does: **grep confirms zero lines changed
  in `middleware/auth.go` or `route.go`'s guards** for this phase.
- **Login auto-selects when there is no ambiguity, and refuses to guess
  otherwise.** Exactly one usable grant becomes the active context
  automatically — the ordinary case, one person, one role, one place. Two or
  more issues a token with `Aktif: nil`, which authorizes nothing at all until
  `switch-context` is called. There is no default among several grants that
  would not risk someone acting under an authority they did not realize they
  had picked, so none is chosen. A session with `Aktif == nil` can still reach
  `auth/me` and `switch-context` — both are open to any authenticated caller
  and need no special case for this, since neither carries a `RequireRole`
  guard to begin with.
- **`attachRolesForLogin` now filters on unit activity too, via
  `grantUsableBy`.** Before phase 3 it only excluded a retired role; a grant
  scoped to a retired `unit_kerja` is equally unusable and must not become the
  active context, appear in the switcher menu, or count toward "exactly one
  grant". `grantUsableBy` is the single predicate both `Login`'s filtering and
  `SwitchContext`'s validation share, so the two can never drift on what
  "usable" means.
- **`SwitchContext` re-reads the grant from the database — the one place in
  the whole design a token's claims are not trusted.** Everywhere else,
  authorization is purely a claims read, which is the entire point of
  choosing stateless JWT. Here it cannot be: the caller is naming a grant by
  id, and a stale token could name one that has since been revoked, or whose
  role or unit has since been retired. `UserRepository.FindGrantByID` +
  `grantUsableBy` (ownership, role active, unit active-or-absent) is that
  check, and it runs against the database at request time — a deliberate,
  narrow exception to "no per-request lookup", scoped to exactly one endpoint.
- **Every rejection reason collapses to one 403.** Grant does not exist,
  belongs to another user, role retired, unit retired — `SwitchContext`
  answers `model.Forbidden("grant does not exist or is not usable")` for all
  four, the same reasoning `Login` already applies to an unknown username:
  distinguishing them would let a caller probe which grant ids exist for
  other users.
- **`SwitchContext` takes a `userID int64`, not a `*model.Session`.** The
  usecase layer needing only the caller's id — not the whole session object —
  is what it actually needs; the controller extracts it via
  `middleware.SessionFrom` and passes the bare id down, the same shape
  `product_controller.go` already uses for `created_by`.
- **Switching context cannot revoke the token being switched away from, and
  is not trying to.** `SwitchContext` mints a brand new token; the old one is
  still signed, still unexpired, and still authorizes exactly what it did the
  moment it was issued. This is not a bug being tolerated — it is the same
  "tokens cannot be revoked" limitation stated in "Authentication and
  authorization" above, restated here because switch-context is exactly where
  someone might assume otherwise. `jwt.ttl_minutes` is the only bound, same as
  everywhere else. **Do not "fix" this with a Redis blacklist** — same
  rejection as the general case, for the same reason: it reinstates the
  per-request lookup JWT was chosen to avoid.
- **This is not a security boundary against the token's own holder.** Active
  context is a *least-privilege and clarity* control — it stops someone from
  acting under an authority they forgot they were holding, not from a caller
  who already has a token doing anything that token's claims allow. Holding a
  token that carries a CASHIER-at-outlet-A grant and an INVENTARIS-at-outlet-B
  grant means both are yours; switching only changes which one is *active*,
  never what you are entitled to switch to.
- **`TestAlgNoneTokenIsRejected` and `TestRouteGuardsRunBeforeHandler` needed
  no changes** beyond updating the literal claims/session values they
  construct to the new shape — pinned in the DoD precisely because a passing
  test here is what proves phase 4 did not quietly touch the security
  primitives the issue promised to leave alone.

Phase 4 ends here. Phase 5 — validating `id_ruang` against the active unit —
is its own section below. Phase 6 — read-path scoping, built afterwards once
it was proven necessary — is the section after that.

### `id_ruang` validated against the active unit (isu #12 fase 5, no migration)

The last phase. `unit_kerja → ruang` is one-to-many — an outlet with both a
warehouse and a shopfront has two rooms in one unit — so knowing the active
unit is never enough to infer *which* room a document means. The client still
picks; what changes is that the id it sends is checked, not merely offered
from a filtered list.

- **Two decisions were made before writing any of this, both explicit
  trade-offs rather than defaults, and both open questions in the issue
  itself:**
  - **Write paths only, at the time. Reads stay exactly as open as before —
    for now.** Every list and get endpoint is unfiltered by unit at the end
    of this phase, same as every phase before it. The issue frames this as
    the two-day-versus-three-week fork and recommends exactly this for a
    first pass — scoping reads means touching every list endpoint's filter
    constant and its `COUNT` twin, and one missed pair makes `total_item`
    disagree with the rows. Reads were scoped afterwards, once asked for, as
    phase 6; see that section for what changed and what didn't need to.
  - **`mutasi` checks `id_ruang_asal` only, never `id_ruang_tujuan`.**
    Phase 1 already decided cross-unit transfers are allowed, and the active
    unit is exactly one unit — requiring *both* rooms to match it would make
    every mutasi this check permits same-unit in practice, quietly reversing
    that decision. Checking the source room alone matches the real
    authority being asserted: an INVENTARIS person is vouching that goods are
    leaving a room they are responsible for; where those goods land is not a
    claim about their own authority.
- **`periksaRuangUnitAktif` (`usecase/shared.go`) is the one function every
  call site shares**, the same role `periksaPeriode` plays for closed
  months — except this one guards nothing on the database side, because
  `ruang.id_unit_kerja` cannot change after a room is created (`ruang` has no
  `PATCH`). There is no race to be defensive about; the check is simply
  correct once and stays correct. A `nil` `aktifIDUnitKerja` — the caller's
  active grant applies everywhere, or (defensively) there is no active
  context at all — skips the check entirely, the same reading `id_unit_kerja
  IS NULL` already carries everywhere else in this codebase. An unknown
  `id_ruang` is deliberately let through (returns `nil`): that failure
  belongs to the foreign key, not to this check.
- **Validation, not a default — the same rule `created_by` already
  follows.** `AktifIDUnitKerja *int64` rides on `CreatePembelianRequest`,
  `CreateMutasiRequest`, and `UpdateMutasiRequest`, filled by the controller
  from `session.Aktif.IDUnitKerja` via the new `aktifIDUnitKerja(ctx)`
  helper — never from the body, and never used to silently substitute a room
  the client didn't ask for. A server that filled in a default while still
  accepting whatever `id_ruang` the body sent would make the scoping
  decorative; `id_ruang` is used exactly as sent, or the write is refused
  403.
- **Only two modules have `id_ruang` in their own request body at all —
  `pembelian` and `mutasi` — so only they gained this check.**
  `penerimaan_susulan` and `retur_pembelian` copy `id_ruang` from the parent
  `pembelian`'s own header, never accept one in their own body, so the
  parent's Create already validated it; checking again on the child would be
  redundant at best and wrong if it ever re-read a stale copy. `pembelian`'s
  own `PATCH` has no `id_ruang` field to begin with (unchanged from before
  this issue), so only `Create` needed the check there; `mutasi` needed it on
  both `Create` and `Update`, since "both rooms may change while `DRAFT`" (see
  "Mutasi antar ruang" above) already lets `id_ruang_asal` move after
  creation.
- **`mutasi`'s `Update` only checks when `id_ruang_asal` is actually present
  in the patch, and only the new value** — mirroring the existing
  `id_ruang_asal <> id_ruang_tujuan` check just above it, which is checked
  against the *effective* value (patch value if present, else the stored
  one) for the identical reason: a patch that never touches the field cannot
  have broken anything, and re-validating a stored value that cannot have
  changed (`ruang` has no `PATCH`) would be pure waste.
- **`RuangRepository.IDUnitKerjaByID`** is a one-column read, not
  `FindByID`'s join — this check needs only the unit id, and pulling `kode`,
  `nama_ruang`, `is_aktif`, and a name lookup along for a fact nobody asked
  for would be waste on a path every `pembelian`/`mutasi` write now takes.
- **`PembelianUseCase` and `MutasiUseCase` each borrow `RuangRepository`** for
  exactly this one query, the same "borrow a repository for a narrow read"
  shape `UserUseCase` already uses for `UnitKerjaRepository` and
  `SupplierUseCase` for `PembelianRepository`.
- **`users.id_ruang_default` was deliberately not built.** The issue raises
  it explicitly as a possible convenience — saving the client one field on
  every request — and just as explicitly warns not to confuse it with this
  authorization boundary: one saves typing, the other is what stops a write
  from naming a room outside the caller's authority. Add it only if the
  convenience is actually asked for, and never let it double as validation.
- **This does not close isu #12 outright** — the five load-bearing phases are
  built: the decisions, the `unit_kerja` master and `ruang.id_unit_kerja`,
  `user_role.id_unit_kerja` with its `NULL`-safe diff, active session context
  via `switch-context`, and `id_ruang` validated against it. What the issue
  left explicitly optional (phase 6, "baru kalau terbukti perlu") bundled
  three independent pieces; read-path scoping was the one asked for and is
  its own section next. `users.id_ruang_default` and a role-as-snapshot
  column on transaction documents remain deferred, not overlooked.

### Read-path scoping (isu #12 fase 6, no migration)

The optional phase, built after being explicitly asked for rather than
inferred as "obviously also needed." The issue bundles three independent
pieces under phase 6 and says to build none of them until proven necessary;
only the first was — the other two (`users.id_ruang_default`, a
role-as-snapshot column on transaction documents) are still deferred.

- **What "scoped" means depends on the shape of the read, and conflating
  them is the mistake to avoid.** A `Get` answers **404**, not 403, for a
  resource outside the caller's active unit — the same one the room's own
  foreign key would produce for an id that never existed, on purpose: a
  scoped read has to make "outside your unit" and "does not exist" the same
  fact from the caller's side, or the 403 case itself confirms the resource
  is real. A `List` (and `product/{id}/stok`, which has list shape) simply
  **omits** rows outside the unit, silently, the same way a page with no
  matches always looks — there is no id to 404 against and nothing to
  confirm or deny.
- **`diLuarUnitAktif` (`usecase/shared.go`) is the read-side counterpart of
  `periksaRuangUnitAktif`** from phase 5, and deliberately a separate
  function rather than a shared one: the write-side check returns
  `model.Forbidden`, and reusing it for reads would make a scoped Get leak
  exactly the fact it exists to hide. `diLuarUnitAktif` returns a bare
  `bool`; every call site maps `true` to `model.NotFound` itself, so the
  403-shaped mistake has to be written out loud at each site rather than
  inherited by accident. A `nil` `aktifIDUnitKerja` excludes nothing — the
  same reading `nil` carries everywhere else in this codebase.
- **Every scoped module gained one column read alongside the document
  already being read, never a second query.** `pembelian`, `penerimaan_susulan`,
  and `retur_pembelian` each carry an unexported `IDUnitKerjaRuang int64` on
  their entity, filled by joining `ruang.id_unit_kerja` into the same read
  query that already joins `ruang` for its name — `pembelianReadColumns`,
  `susulanReadColumns`, and `returReadColumns` each gained one column, and
  their `scanXRead` gained one scan target. `mutasi` is the same shape but
  named `IDUnitKerjaRuangAsal`, joined from `asal.id_unit_kerja` rather than
  `ruang.id_unit_kerja` — see below for why only that one room.
- **The `detail()` helper every write-path re-read goes through is where the
  check actually lives, threaded as a parameter rather than read from
  ambient state.** `pembelian`, `penerimaan_susulan`, `retur_pembelian`, and
  `mutasi` each already had a `detail(ctx, db, id)` helper — `Get` was one of
  several callers, alongside every posting/approval action that re-reads its
  own document to build a response. All four gained an `aktifIDUnitKerja
  *int64` parameter: `Get` (and, on `pembelian`, `Sisa`) passes the caller's
  real active unit; every write-path call — `Create`'s re-read, `Ajukan`,
  `Posting`, `Tolak`, `Batal`, `ReplaceDetail`, `BagiRataKoli` — passes `nil`.
  The reasoning is the same as `periksaRuangUnitAktif`'s write-side check
  running only on the fields actually being written: a caller who just
  posted a document is, by construction, allowed to see the response posting
  produced, whatever their active unit is at that moment. Scoping the
  write-path re-read would make a legitimate action's own response look like
  a 404.
- **A filter clause that reaches a joined table forces the `COUNT` query to
  join it too, and three of the four modules had to change for exactly
  that.** `pembelianFilter`, `susulanFilter`, and `returFilter` each gained a
  `$N::BIGINT IS NULL OR ruang_alias.id_unit_kerja = $N` clause, and each
  module's `Search` had been running its `COUNT` against a bare `FROM table`
  that never reached `ruang` — because nothing before phase 6 needed to.
  Changing the `COUNT` query to use the same `xFrom` constant as the row
  query is what "write the filter once, and both queries use it" (see
  "PostgreSQL specifics" below) actually requires once the filter itself
  reaches a join. `mutasi` needed the identical fix to its own `Search`,
  even though `mutasiFrom` already joins both rooms unconditionally for the
  room *names* — the `COUNT` query had never used `mutasiFrom` either, for
  the same reason.
- **`mutasi` checks `id_ruang_asal` only, never `id_ruang_tujuan` — the read
  side inherits the identical asymmetry phase 5 already made on the write
  side, for the identical reason.** Requiring both rooms to match the active
  unit would make every mutasi this scoping lets through same-unit in
  practice, quietly re-deciding that cross-unit transfers are allowed (isu
  #12 fase 1). A transfer whose destination sits in a different unit than
  the caller's stays fully visible to whoever owns the source room; a caller
  who only owns the destination cannot see it at all, even though the goods
  are headed there. That is deliberate: visibility follows the room a
  caller is asserting authority over, not every room a document happens to
  touch. `TestMutasiGetVisibleWhenOnlyDestinationRuangOutsideActiveUnit`
  pins exactly the case a reader arriving from the other three modules would
  expect to be scoped and isn't.
- **`GET /product/{id}/stok` is scoped too, but it is a list-shaped read, not
  a `Get`, and the fix landed in `KartuStokRepository.SaldoPerRuang` rather
  than in a `detail()`-style helper.** The filter sits on the *outer* query,
  after the `DISTINCT ON (ks.id_ruang)` subquery has already picked one row
  per room — filtering a room out earlier could, in principle, only change
  which room wins each `DISTINCT ON` group, and since the key is the room
  itself, it cannot actually change any row that survives. Keeping the
  filter outside is what makes that true by construction rather than by
  argument. Unlike a `Get`, there is no id to 404 against — a room outside
  the active unit is just missing from the list, the same as a room the
  product has never moved through.
- **Query-string spoofing is closed the same way `ActorID` already is.**
  Every `AktifIDUnitKerja` field added by this phase carries `json:"-"` on a
  body-bound request or `query:"-"` on a query-bound one — Fiber v3's query
  binder is `gorilla/schema` under an alias tag, and `query:"-"` is what
  excludes a field from it, confirmed against the vendored binder source
  rather than assumed. The controller then overwrites the field
  unconditionally after binding, from `aktifIDUnitKerja(ctx)` (the existing
  helper from phase 5), regardless of whether the tag alone would have been
  enough — the same defense-in-depth `ActorID` already gets from
  `middleware.SessionFrom`.
- **Tests live in `fase6_read_scope_test.go`, one file spanning every scoped
  module, rather than one file per module** — unlike phase 5, whose write-side
  tests split naturally along `pembelian`/`mutasi`. A read scoped identically
  across five different resource shapes is one behavior repeated five times,
  and reading the five Get/List pairs together is what makes the one real
  asymmetry — `mutasi`'s source-only check — legible as a deliberate
  exception rather than a module that was simply forgotten.

### Stok per ruang (no migration)

`GET /api/v1/product/{id}/stok` — isu #7 fase 1, and the **first read of `kartu_stok` in the codebase**. `KartuStokRepository` had only `Insert`, `FindByRef`, and `HasRef`; nothing needed a balance until now, because purchases and follow-up receipts only add and a return's quota comes from an invoice line.

- Three repository methods, built as their own phase because everything after this wants them: `SaldoTerakhir` (one pair), `SaldoBatch` (many pairs, one query, `unnest` of two arrays like `FindFaktorBatch`), and `SaldoPerRuang` (one product across rooms, backing the endpoint). `penjualan`, `pemakaian`, and `stok_opname` will each need the same thing.
- **A pair with no rows is a balance of zero, not a missing record.** Same reading the trigger takes when it COALESCEs the previous row, and the same shape `periode` uses for a month nobody has closed. `SaldoTerakhir` returns the zero value; `SaldoBatch` simply omits the key.
- **It is a read and never a guard**, and every call site has to keep treating it that way. The balance is decided inside the trigger under an advisory lock precisely so no reader can get in front of it.
- Follows `riwayat_beli`: no table, no migration, the query lives in the repository that owns `kartu_stok`, and `ProductUseCase` borrows it for the resource the endpoint hangs off. **No pagination** — one row per room the product has moved through, `ruang` is small, and every caller wants all of them to pick a source room from.
- Rooms the product has never been in do not appear; a room that emptied out still does, with zero. Unknown product is a 404, a product that has never moved is an empty list.
- **`SaldoPerRuang` additionally scopes by the caller's active `unit_kerja` since isu #12 fase 6** — see "Read-path scoping" above for the mechanism and why the filter sits outside the `DISTINCT ON` subquery rather than inside it.

### Riwayat harga beli (no migration)

`GET /api/v1/product/{id}/riwayat-beli` — isu #4 fase 4, and the replacement for the purchase order this system deliberately does not have. It is the worked example of a **read that is not a module**: no table, no migration, no DTO to fill in, nothing that can fall out of step. Copy this shape rather than a slice when a request is answerable from documents that already exist.

- **Nothing new is stored.** Every POSTED `pembelian_detail` row is already a price that was actually paid, which is worth more than a quotation because a quotation is only what was promised in a chat. Adding a table for this would create a second source of truth for a fact the first one already carries.
- **The SQL lives in `pembelian_repository.go`, the endpoint hangs off product.** `ProductUseCase` borrows `PembelianRepository` the way `UserUseCase` borrows `RoleRepository` — the query is over another module's tables, so it stays in that module's repository. A usecase of its own would be a module for one query.
- **Two prices, and collapsing them into one is the mistake to avoid.** `harga_satuan_dasar` (= `subtotal / qty_dasar`) is the invoice per base unit and is what a supplier's next quote is compared against; `harga_pokok_satuan_dasar` is after the nota discount, PPN share, and freight, and is what margin is judged against. Negotiating with the second holds the supplier responsible for the carrier's bill; costing with the first drops freight entirely.
- `harga_satuan_input` is reported but is **not comparable**: it is per input unit, so 120.000/DUS and 10.000/PCS look nothing alike while being the same price.
- **The product is looked up first**, so an unknown id answers 404 and a product nobody has bought answers an empty page. Those are different facts and a client that cannot tell them apart shows the wrong message.
- **Only POSTED.** A DRAFT is a typed page and a BATAL is a purchase that was withdrawn; neither is a price anyone paid. One condition covers both, rather than a second clause excluding BATAL.
- `DISTINCT ON (p.id_supplier)` is what makes it one row per supplier, and it forces the inner `ORDER BY` to lead with `id_supplier` — which is not a useful reading order, hence the wrapping query that re-sorts by date. The outer `ORDER BY` ends in `id_supplier`, unique across the subquery *because* `DISTINCT ON` made it so. The inner tiebreaker `d.id DESC` matters: one document may carry the same product twice.
- The division is safe because `pembelian_detail_qty_dasar_check` makes `qty_dasar > 0`. Casting to `NUMERIC(20,4)` rounds halves away from zero, matching `formatNumeric` — a figure computed in SQL and one recomputed in Go have to agree.

### Product, units, and versioned prices (migration 000011)

Three tables, one slice: `product`, `product_satuan`, `product_harga_jual`.

- **The base unit is inserted by the usecase, not the caller** — `faktor = 1`, from `id_satuan_dasar`, in the same transaction as the product. Nothing in the schema enforces it, so a product that slipped through without one would break every conversion built on it. A caller who lists the base unit again collapses into that row; listing it with any other factor is a 400.
- **`product_satuan.faktor` is `BIGINT`.** Conversions must be whole numbers. A unit holding 2.5 base units cannot be represented, and silently rounding it would corrupt stock arithmetic.
- **`berlaku_sampai` is exclusive and `NULL` means open-ended**, because `product_harga_jual_no_overlap` ranges over `daterange(berlaku_dari, berlaku_sampai, '[)')`. Closing a version means setting it to the **next version's start date**, not the day before — that leaves neither gap nor overlap.
- `CloseOpenHargaJual` guards with `berlaku_dari < $3`. Without it, a version starting on or after the new date would be closed to a date at or before its own start, violating `product_harga_jual_periode_check`. A pre-existing future price is a real case, not hypothetical.
- **Overlap is caught only by the GiST exclusion constraint** (`23P01` → `repository.IsExclusionViolation` → 409). The check spans rows, so no pre-check in Go can replace it: two concurrent requests can both find no overlap and both insert.
- **`is_default_input` is capped at one per product** by a partial unique index from `000011`. Setting a new default therefore *moves* the flag — `ClearDefaultSatuan` runs first, in the same transaction. Two flagged units in one create request are rejected in Go first, so the message names the field rather than an index.
- A price may only be set for a unit already in `product_satuan`. **No foreign key ties `product_harga_jual.id_satuan` to `product_satuan`**, so the usecase has to check it.
- `kode_barang` and `id_satuan_dasar` are **absent from the update DTO** and must stay that way. `kode_barang` identifies the item across every document referencing it; `id_satuan_dasar` would invalidate every `faktor` and every quantity already posted to `kartu_stok`.
- `InsertSatuan` uses `ON CONFLICT ... DO UPDATE`, not `DO NOTHING`: a success response must never mean the stored factor disagrees with the request.
- `harga` is scanned as `harga::TEXT`. `NUMERIC(20,2)` into a `float64` rounds money on the way out.
- Detail is three queries (product, units, prices); list is two and carries **no** children, with the keys omitted rather than empty.

### Users and roles (migration 000010)

One user holds many grants. `user_role` is the only record of that. What a user *may* do is still the union of every grant they hold — `RoleRepository`/`UnitKerjaRepository` validate against all of them, and the management view (`GET /api/v1/user`) lists all of them. What a **session** authorizes as, since isu #12 fase 4, is exactly one of them at a time — see "Konteks aktif per sesi" above. Don't conflate the two: a user's grants are a set; a session's active context is a single choice from that set.

- **`users.role_active` is gone.** Migration `000002` declared `UNIQUE (role_active)`, which is unique across the whole table rather than per user, so the system could hold exactly one cashier and the second was rejected by the database. Its FK also pointed at `user_role (id)` without `user_id`, so user A's active role could point at user B's grant. Migration `000010` drops the column outright rather than repairing it. Don't reintroduce it; if a "default module on login" preference is ever wanted, that is a UI preference column, not a permission gate. See "Wewenang bertempat" above for why `user_role.id_unit_kerja` (migration `000020`) is not this column resurrected under a new name.
- **Roles are seeded, then editable.** `SUPERADMIN`, `CASHIER`, `INVENTARIS` come from `db/seeder_postgres/003_role.sql`. `PATCH /api/v1/role/{id}` can rename them, and **renaming a role that authorization code checks by name breaks that code** — nothing in the database can catch it. Retire with `is_aktif = false` and add a new role instead.
- **A grant is `(role, unit_kerja)`, not just role, since migration `000020`.** `grants` on `POST`/`PATCH /api/v1/user` replaces the whole set: absent leaves grants alone, `[]` revokes everything, a list ends with exactly those grants. An explicit `null` is rejected, because `[]` already says "no grants". Each entry is `model.GrantRequest{IDRole, IDUnitKerja}` — `IDUnitKerja` nil means the role applies everywhere. `Optional[[]model.GrantRequest]` is the DTO, and it must stay registered in `config.NewValidator` alongside the other `Optional` instantiations.
- **The same role may be granted more than once per user, one row per unit.** "INVENTARIS at outlet A" and "INVENTARIS at outlet B" are two distinct `user_role` rows, not a duplicate — `usecase.toGrants` deduplicates by the `(id_role, id_unit_kerja)` **pair**, not by role id alone, so sending both in one request grants both. `entity.User.Roles` reflects this: it is `[]entity.RoleGrant`, and the same role's name can legitimately appear more than once in it.
- **`ReplaceRoles` is a diff, not delete-then-insert.** Rows for grants that survive the change are left alone so `user_role.created_at` keeps saying when the grant actually started. The comparison is **`NULL`-safe** (`IS NOT DISTINCT FROM` on `id_unit_kerja`, inside a `NOT EXISTS` anti-join), not `<>` — `id_unit_kerja <> ...` is `NULL` (never `TRUE`) whenever either side is `NULL`, so a naive port of the old `role_id <> ALL($2)` pattern would silently keep every global grant no matter what. See "Wewenang bertempat" above for the full trap and how the insert side works around the same limitation by using two `ON CONFLICT` statements instead of one.
- `user_role` is the one table in the codebase where `DELETE` is correct. It is a join table that no transaction table references, so revoking a grant breaks no foreign key and erases no document history — `created_by` on documents points at `users`, not at `user_role`.
- **A roles-only patch still has to move `updated_at`**, which is what `UserRepository.Touch` is for: it writes no other column, fires the `users_set_updated_at` trigger, and yields `sql.ErrNoRows` so an unknown id still answers 404.
- **Role ids and unit ids are both validated before the write**, not left to the foreign key: the FK cannot tell a retired row from a live one, and its message names a constraint rather than the field. `RoleRepository.CountActiveByIDs` and `UnitKerjaRepository.CountActiveByIDs` each compare a count against the number of **deduplicated** ids for their own kind — pass duplicates and a valid request is wrongly rejected. The two checks are independent and produce different messages, so an operator learns which field was wrong. `repository.IsForeignKeyViolation` (SQLSTATE `23503`) is the race backstop for both, mapping to a 400.
- **Passwords are bcrypt hashes, hashed in the usecase.** `model.UserResponse` has no password field at all, which is what makes a leak structurally impossible rather than a matter of remembering. bcrypt refuses input over 72 **bytes** while the DTO's `max=72` counts **runes**, so `hashPassword` maps `bcrypt.ErrPasswordTooLong` to `model.Invalid` for the multibyte case.
- Attaching grants to a page of users is **one extra query, not one per user** (`FindRolesByUserIDs` with `= ANY($1)`, `LEFT JOIN unit_kerja`). `pgx/stdlib` implements `CheckNamedValue`, so a Go `[]int64` (and, since fase 3, `[]*int64` for a nullable `BIGINT[]`) passes through `database/sql` untouched — no array wrapper needed.
- The `role_id` list filter is an **`EXISTS`, never a join**. A join to `user_role` returns one row per matching role and silently multiplies the page when a user holds several, breaking both `LIMIT` and `total_item`.
- A user's grant list **includes grants whose role, or whose unit, was retired after being granted** — the grant is still real and still needs revoking. `RoleRef.is_aktif` tells a retired role apart; `RoleRef.is_aktif_unit_kerja` (fase 4) is its counterpart for the unit, nil exactly when `id_unit_kerja` is nil. This management view intentionally shows *everything held*, retired or not — the filtering down to what a session may act as happens only in `AuthUseCase.attachRolesForLogin`/`grantUsableBy`, never here.
- `username`, `email`, and `role.nama` are unique **case-insensitively** via `lower(...)` indexes, same as master codes. `email` is nullable, so any number of users may have none.
- **The JWT claim carries every usable grant, plus which one is active — see "Konteks aktif per sesi" above for the full design.** It is not a flat role-name list any more; `model.Grant`/`model.ActiveContext` are the shape, shared by the claims, `Session`, and the login/switch-context responses alike.

### Inventory data model (migrations 000002–000008)

`pembelian`, `penerimaan_susulan`, `retur_pembelian`, `pembayaran_utang`, and `mutasi` now exercise this schema end to end; sales, sales returns, `pemakaian`, stock opname, and the receivable side still have **no Go code**. Read these invariants before writing any inventory usecase; several are enforced by the database and will reject wrong code at runtime. `internal/usecase/pembelian_usecase.go` is the worked example of obeying them.

- **`kartu_stok` is the only source of truth for stock and inventory value.** No master table carries a stock column. Never compute stock by summing documents.
- **It is append-only, enforced by trigger.** `UPDATE`, `DELETE`, and `TRUNCATE` all raise. Corrections are new reversing rows that fill `id_kartu_stok_asal`.
- **The trigger computes the balance, not the application.** On insert, `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, and `nilai_akhir` are all overwritten. A usecase supplies only the direction (`stok_masuk` **or** `stok_keluar`, never both), `nilai_masuk`, and the reference columns. Sending a computed balance is silently discarded — don't rely on it.
- **Moving average:** incoming rows shift `harga_pokok_satuan`; outgoing rows never do. Stock reaching zero forces `nilai_akhir` to exactly 0 so rounding residue cannot accumulate.
- Balance is partitioned by `(id_barang, id_ruang)` and ordered by **`id`, not date**. Inserts take a `pg_advisory_xact_lock` on that pair, so concurrent postings for the same product+room serialize.
- The trigger raises on negative stock and on posting into a `periode` with status `TUTUP`. A month with **no** `periode` row counts as open. Since `000017` it also takes a shared advisory lock on `(tahun, bulan)` before reading the status, which is what stops a book closing from overtaking a posting already in flight — see "Periode and book closing".
- Quantities in `kartu_stok` are always in the base unit. `qty_input`/`id_satuan_input` are an audit trail of what the operator typed.
- Documents store **snapshots** (`faktor_konversi`, prices, HPP); master data stores current rules. Master may change; snapshots never do. Returns must copy cost from the originating detail row, not from the current average — otherwise a sale and its return do not cancel out.
- Stock moves only on posting, never on draft.
- **Receivables and payables are mirror images.** `penerimaan_pembayaran`/`pembayaran_alokasi` face customers; `pembayaran_utang`/`pembayaran_utang_alokasi` face suppliers. Same rules on both sides, money flowing the other way. Payment↔invoice is many-to-many, which is why allocation is its own table.
- `penjualan.status_pembayaran` and `pembelian.status_pembayaran` are **caches**. Recompute both from POSTED allocations and POSTED returns; never let a form set them directly. Remaining balance = document total − allocations − returns, counting POSTED rows only.
- **An uncashed giro is not a payment.** The balance drops only when `status_giro` becomes `CAIR`.
- **Overpayment is normal**: `jumlah_dialokasikan` may be less than `jumlah`; the remainder sits as a credit for later invoices. Never force allocation to balance exactly.
- **Freight is allocated by koli** (`metode_alokasi_angkut` defaults to `'KOLI'` since migration `000008`): `alokasi_biaya = (jumlah_koli / total_koli) × biaya_angkut`, where `biaya_angkut = total_koli × tarif_per_koli`. `jumlah_koli` is decimal because one line can occupy part of a koli. When every `jumlah_koli` is zero, fall back to `QTY` rather than dividing by zero. The method is stored per document, so changing it later never rewrites old allocations — those are frozen in `alokasi_biaya`.

Enforced by CHECK constraints (so don't re-validate in Go, just surface the error): one-way movement, non-negative quantities and values, `faktor > 0`, `qty_dasar > 0`, `jumlah_koli >= 0`, `mutasi` source ≠ destination, requester ≠ approver in `pemakaian`, `bulan` 1–12, `jumlah > 0` and `jumlah_dialokasikan <= jumlah` on both payment tables, and non-overlapping `product_harga_jual` validity ranges (a GiST exclusion constraint needing `btree_gist`).

Still **application-side only** (section E of the design notes) — the database will not catch these:

- ~~`product_satuan` must include the base unit with `faktor = 1`~~ — done, enforced in `ProductUseCase.Create`
- ~~`jumlah_koli` across details must equal the header's `total_koli` before a purchase may post; offer a "bagi rata" button that splits `total_koli` proportionally to `qty_dasar`~~ — done, `siapkanPosting` + `POST /pembelian/{id}/bagi-rata-koli`
- ~~`alokasi_biaya` must sum exactly to `biaya_angkut`; push the rounding remainder onto the line with the largest `jumlah_koli`~~ — done, `bagiProporsional`
- ~~posted documents must reject detail edits~~ — done for `pembelian`, `penerimaan_susulan`, `retur_pembelian`, `pembayaran_utang`, and `mutasi`, via `kunciDenganStatus`. Still owed by every other document type
- ~~cancelling a posted document writes reversing `kartu_stok` rows with `id_kartu_stok_asal` set~~ — done for `pembelian`, `retur_pembelian`, `penerimaan_susulan`, and `mutasi`. **The "HPP copied from the original" half of that rule is not achievable** and was dropped: the trigger overwrites `harga_pokok_satuan` and `nilai_keluar` on every outgoing row, so reversals take the current moving average. See "Pembelian and the posting engine"
- ~~a follow-up receipt must not exceed the outstanding amount (`qty_dasar − qty_diterima_dasar − Σ susulan`)~~ — done, `periksaSisa`, re-checked at posting under the purchase's row lock
- ~~cumulative return qty must not exceed the source document's qty~~ — done for `retur_pembelian`, `periksaDapatDiretur`, re-checked at posting under the purchase's row lock. Note the ceiling is what **arrived** (`qty_diterima_dasar + Σ susulan − Σ retur`), not the invoice quantity. Still owed by `retur_penjualan`
- ~~a POSTED return must reduce the payable~~ — done, via `nilai_kredit_utang` rather than `retur_pembelian.total`. See "Pembayaran utang and the payable side"
- ~~allocation must not exceed the payment amount or the document's remaining balance~~ — done on the **payable** side, and re-checked at giro clearing rather than only at posting. Still owed by `penerimaan_pembayaran`
- ~~cancelled documents must not accept allocations~~ — done on the payable side: an allocation's invoice must be POSTED, and a purchase with payments against it cannot be cancelled. Still owed on the receivable side
- ~~`pembelian.status_pembayaran` must be recomputed rather than set from a form~~ — done, `RecalculateStatusPembayaran`, called from every path that can change the answer. Still owed by `penjualan.status_pembayaran`
- credit sales must respect `plafon_kredit`
- ~~a payment, all its allocations, and every touched `status_pembayaran` must be written in one database transaction~~ — done on the payable side

Everything left in this list is on the **sales/receivable** side. The payable side is finished, and it is the mirror to copy rather than a shape to invent.

The daily reconciliation job over the balance chain (section F) is also not built.

### Adding a module

Follow the `supplier` slice, in this order: migration in `db/migrations_postgres/` (most inventory tables already exist — check first) → `entity` → `model` DTOs → `model/converter` → `repository` (methods take `DBTX`) → `usecase` (validate, own the transaction) → `delivery/http` controller → register in `route.RouteConfig` → wire in `config.Bootstrap` → update `docs/openapi.yaml`.

If the slice writes more than one table, follow `user` instead — it is the worked example of a usecase holding two repositories and committing both tables in one transaction.

If the slice is a **transaction document** — one with a status, a generated number, and stock movements — follow `pembelian`. Copying a master slice for it will leave out the row lock on every transition, the exact-decimal arithmetic, and the reuse of `DocumentCounterRepository` and `KartuStokRepository`.

If the document **derives from a POSTED one** — a follow-up, a return, anything pointing at another document's detail rows — follow `penerimaan_susulan` or `retur_pembelian` rather than `pembelian`: the parent's row lock, the copied cost snapshot, the quota re-checked at posting, and the per-document unique index on the source line are what those two add. Take the one whose goods move the same direction as yours, and check the "Retur pembelian" notes for which parts are direction-specific.

If the document has a status and a number but moves **no stock** — a payment, an allocation, anything purely financial — follow `pembayaran_utang` rather than `pembelian`: no approval state, no `KartuStokRepository`, and caches recomputed from one statement instead of reversed.

If the request is answerable from rows that already exist — a report, a history, a comparison — do **not** add a slice at all. Follow `riwayat_beli` or `utang_supplier`: an entity for the projection, a query in the repository that already owns those tables, and a method on whichever usecase owns the resource the endpoint hangs off. No migration, no DTO to fill in, nothing to keep in step.

If the slice is keyed on something that is not an `id` — a natural key the schema already declares unique — follow `periode`: routes on that key, no surrogate id in the response, and a synthetic answer for the key that has no row yet. It is also the model for a refusal that other modules inherit through the database rather than through a call.

If the document moves stock **in two directions at once**, or writes `kartu_stok` without an approval stage, follow `mutasi`: the incoming row valued from the outgoing row's `RETURNING`, `KartuStokRepository.KunciSaldo` before the first insert, and `PeriodeRepository.LockShared` before that.

If the slice's data is a **file** — anything whose bytes live outside PostgreSQL — follow `dokumen`: the store goes behind an interface in `internal/repository`, the usecase owns the ordering between the file and its row, and whatever can be left behind is reconciled by a worker job working from rows.

Master data gets no `DELETE`: every master table is referenced by transaction tables, so deleting a used row either fails on a foreign key or destroys the audit trail. Retire rows with `is_aktif = false` instead. Two tables are exceptions: `user_role`, for the reasons in "Users and roles", and `dokumen`, whose `DELETE` is soft — the row stays with `deleted_at` set and only the file goes.

### PostgreSQL specifics

Since there is no ORM, these are hand-written every time — get them right:

- Placeholders are `$1, $2, …`, not `?`.
- `LastInsertId()` is unsupported by the driver. To get a generated key, use `RETURNING id` with `QueryRowContext(...).Scan(&id)`.
- Always use the `…Context` variants and thread the `context.Context` through. Note the limit: Fiber v3's `c.Context()` defaults to `context.Background()` and is **not** cancelled when the client disconnects, so a slow query is not aborted for free — attach an explicit timeout where one matters.
- Identifiers fold to lowercase unless double-quoted; keep table and column names lowercase `snake_case` so quoting is never needed.
- Use `TIMESTAMPTZ` (not `TIMESTAMP`) for anything representing a real point in time.
- `updated_at` is maintained by the `set_updated_at()` trigger installed in migration `000001`; reuse it for new tables rather than setting the column from Go.
- **Every `ORDER BY` paired with `LIMIT`/`OFFSET` ends in a unique column** (`ORDER BY nama, id`). Ordering on a non-unique column alone lets one row appear on two pages while another is never returned — a live bug the moment data outgrows a single page, not a theoretical one.
- **Every search string goes through `repository.EscapeLike`** before it becomes a query argument. Unescaped, a user's `%` matches everything and a product literally named `100%` can never be found. This is a correctness bug, not an injection one — `$1` is safe either way.
- **Write the filter once** as a package-level constant and use it for both the `COUNT` and the row query. Two copies drift, and then `total_item` disagrees with the rows. Keep the filter on `$1..$N` with pagination placeholders after it.
- **Never `SELECT *`.** Declare the column list as a constant so `Scan` order cannot drift from it when a migration adds a column.
- Nullable columns must be scanned into a pointer or a `sql.NullXxx`; scanning `NULL` into a plain `string` fails at runtime, and only once some row actually has the column empty.
- A unique index does not constrain `NULL`s, so any number of rows may share `kode = NULL`. Only call `ExistsByKode` when a kode was actually supplied — a `NULL` check always answers false.
- Uniqueness on master codes is **case-insensitive**, via `CREATE UNIQUE INDEX ... (lower(kode))` in migration `000009`. Existence checks must use `lower(...) = lower($1)` to match, and `ON CONFLICT` in a seeder must name the expression (`ON CONFLICT (lower(kode))`), not the bare column.
- Check-then-insert never guarantees uniqueness; two requests can both pass. `repository.UniqueViolation` maps SQLSTATE `23505` so the loser of that race gets a 409 rather than a 500. The pre-check stays only for the friendlier message.

### PATCH semantics

A pointer cannot tell "field absent" from "field explicitly null", and `COALESCE($n, col)` silently keeps the old value for both — so an operator can never clear a mistyped phone number. Every partial update therefore uses `model.Optional[T]`, whose `UnmarshalJSON` records that the key was present at all:

- Nullable columns: `col = CASE WHEN $n::BOOLEAN THEN $m ELSE col END`, with the flag fed from `Optional.Present`.
- `NOT NULL` columns: `COALESCE` is correct, and an explicit `null` is rejected with `model.Invalid` in the usecase.
- `UPDATE ... RETURNING` supplies the response; `sql.ErrNoRows` from it means the id does not exist. Never `SELECT` first to check — two queries, still racy.
- `id`, `created_at`, and `created_by` never appear in an update DTO. The controller binds the body first and then overwrites `ID` from the path.
- Tags on an `Optional` field must lead with `omitempty`, and each instantiation must be registered in `config.NewValidator` — otherwise its validation tags are silently ignored. Registered today: `Optional[string]`, `Optional[bool]`, `Optional[[]int64]`, `Optional[int64]`, `Optional[[]model.GrantRequest]`.
- A collection field works the same way, with the states meaning replace rather than set: absent leaves it alone, `[]` empties it, a list replaces it wholesale. `dive` does reach elements through the custom type func (`TestValidatorDivesIntoOptionalSlice` pins that), so `dive,gt=0` on an `Optional[[]int64]` is really enforced.

## API contract

`docs/openapi.yaml` is the contract. Update it in the same change as any route, request, or response shape change in `internal/delivery` and `internal/model`.

It is also a **build input**, not just documentation: `docs/docs.go` pulls it in with `go:embed` so the server can serve Swagger UI at `/` and the spec at `/openapi.yaml`. Two consequences:

- Dropping `docs/` from the Docker build context **fails compilation** (`pattern openapi.yaml: no matching files found`) rather than merely losing the docs page. `.dockerignore` deliberately does not exclude it and the Dockerfile copies it in.
- A malformed `openapi.yaml` is still served happily — `go:embed` copies bytes and does not parse YAML. `TestEmbeddedSpecIsTheRealContract` only checks the asset arrived and is not empty.

`gofiber/contrib/swagger` is not used: its latest release (v1.3.0) still requires Fiber v2. The page is hand-rolled in `internal/delivery/http/docs_controller.go` and loads Swagger UI's assets from unpkg, so the docs page needs internet access even though the API does not.

`web.swagger` turns it off (`WEB_SWAGGER=false`). When false, `config.Bootstrap` leaves `RouteConfig.DocsController` nil and **neither route is registered** — nil rather than a boolean so the routes cannot be enabled without something to serve them. `NewViper` calls `SetDefault("web.swagger", true)` because `GetBool` answers false for an absent key, which would otherwise make a pre-existing `config.json` silently lose the docs.

`README.md` is a **third** surface that goes stale, not just a front page: it carries its own full endpoint table, authorization matrix, and roadmap. A route change touches `route.go`, `docs/openapi.yaml`, *and* that table.

## Language

Identifiers and schema use the project's Indonesian domain vocabulary (`satuan`, `ruang`, `kartu_stok`, `berlaku_dari`) — keep it; don't translate to English when adding tables or fields. Go comments are English and explain *why*, not what. `README.md` and commit subjects are Indonesian (`feat: modul user & role, lingkungan Docker, dan Swagger di root`); `CLAUDE.md` and code comments are English.
