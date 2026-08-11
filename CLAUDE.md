# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Master data is implemented, and **`pembelian` is the first transaction document** — the first thing that writes `kartu_stok`. Copy an existing slice when adding a module — don't invent a new shape.

- Implemented: **`satuan`**, **`ekspedisi`**, **`supplier`**, **`pelanggan`**, **`role`**, **`user`** (create / get / list / patch), **`ruang`** (create / get / list, no patch), **`product`** with `product_satuan` + `product_harga_jual`, **`pembelian`** with `pembelian_detail`, `kartu_stok` posting, and `document_counter`, and **`penerimaan_susulan`** with `penerimaan_susulan_detail`
- Use **`supplier`** as the template for a plain master slice: it is the one with every ordinary concern at once — nullable unique `kode`, PATCH presence semantics, and a `LEFT JOIN` in the list query
- Use **`user`** as the template when a slice writes two tables: a user and its `user_role` grants in one transaction, and it is where `Optional[[]int64]`, bcrypt hashing, and the `Touch` pattern live. See "Users and roles" below
- Use **`pembelian`** as the template for a **transaction document** — a state machine, a generated number, exact decimal arithmetic, and stock movements, all in one transaction. See "Pembelian and the posting engine" below. Do not model a transaction document on a master slice; the concerns barely overlap
- Use **`penerimaan_susulan`** as the template for a **document that derives from another** — one that points at a parent's detail rows, draws down a quota held there, copies a cost snapshot from it, and rewrites a cache on it. `retur_pembelian` is the same shape with the goods moving the other way. See "Penerimaan susulan" below
- Module path: `Arthafreestyle/ERP` (no domain prefix); internal imports are `Arthafreestyle/ERP/internal/...`
- Go 1.25.0 — required by Fiber v3.4.0, which refuses to build on 1.24
- The rest of the inventory/sales schema exists as migrations `000002`–`000008` and still has **no Go layers** — sales, returns, `pemakaian`, `mutasi`, stock opname, and both payment sides. See "Inventory data model" below
- Auth is implemented: bearer JWT login, role guards per route — see "Authentication and authorization" below
- Not built yet: captcha (Redis is wired but unused), logout/refresh, session revocation (stateless tokens cannot be revoked), any worker job, `periode`
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

`truncateMaster` in `internal/usecase/main_test.go` deletes in dependency order: `kartu_stok` and the purchase tables first, then master tables, then `user_role`, then `users`, then `role`. It uses `DELETE`, not `TRUNCATE` — `TRUNCATE` would cascade into `kartu_stok`, whose guard trigger raises on it. Add new tables to that list, on the correct side — `users` has to come after anything whose `created_by` references it.

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
- `invalidOnCheck` exists mainly for the `kartu_stok` trigger, which raises with `USING ERRCODE = 'check_violation'` for negative stock and for a closed `periode`. A trigger's `RAISE` carries no constraint name, so there is nothing to switch on — each call site supplies its own message, and the database's own text never reaches the client.

Controller boilerplate is fixed — copy it verbatim rather than improvising, because these strings are part of the contract:

- `ctx.Bind().Body(request)` failure → `model.Invalid("malformed request body")`; `ctx.Bind().Query(request)` failure → `model.Invalid("malformed query parameters")`.
- `strconv.ParseInt(ctx.Params("id"), ...)` failure → `model.Invalid("id must be an integer")`.
- Create answers `fiber.StatusCreated`; everything else answers 200 via a bare `ctx.JSON`.
- `WebResponse.Data` deliberately has **no `omitempty`**. An empty slice is "empty" to `encoding/json`, so omitempty would drop the key on exactly the page with no rows and break a client reading `data.length`. The cost is `"data": null` on error responses; keep it.
- Pagination defaults are `PageRequest.Normalize` in `internal/model/model.go`: page 1, size 20, size capped at 100. The usecase calls it; the controller does not.

### Authentication and authorization

Bearer JWT, stateless by decision. Every `/api/v1` route needs a token except `POST /api/v1/auth/login`.

- **Route guards must be the FIRST handler argument.** Fiber v3 registers as `Get(path, handler, handlers...)` and runs the chain in the order given, so `Get(path, controller, guard)` puts the guard *after* the controller — which means never, because a controller does not call `Next()`. The route table looks protected and protects nothing. Write `Get(path, guard, controller)`. `TestRouteGuardsRunBeforeHandler` pins both halves of this, including a subtest that fails if Fiber's ordering ever changes.
- **Tokens cannot be revoked.** Nothing is stored server-side and no lookup happens per request, so there is nothing to invalidate. `is_aktif = false` on a user, or a revoked role, does not reach a token already issued — access ends only at expiry. `jwt.ttl_minutes` (default 60) is the entire bound on that window. Do not "fix" this with a Redis blacklist without revisiting the decision: the blacklist reinstates the per-request lookup that JWT was chosen to avoid.
- **Roles live in the token claims**, so authorization touches no database. The cost is that a role granted or revoked takes effect at next login. Only `is_aktif` roles are embedded, which is where retired-role-does-not-authorize is enforced — `FindRolesByUserIDs` itself returns retired grants on purpose, for the user-management view.
- **`jwt.secret` has no default and the process refuses to start without one** (`config.NewAuthConfig`, minimum 32 characters). A baked-in default is a key every deployment shares, and holding it means minting a `SUPERADMIN` token for any user id. A random per-process key was also rejected: it invalidates every token on restart and breaks outright across more than one instance, both silently.
- **Login answers one message for every failure** — unknown username, wrong password, disabled account. Distinguishing them enumerates valid usernames. The unknown-username path runs a dummy bcrypt compare so it does not return measurably faster than a wrong password, which would leak what the identical message hides.
- `Authenticate` pins the accepted signing method. Without `jwt.WithValidMethods`, the parser trusts the token's own `alg` header and accepts `alg=none`. `TestAlgNoneTokenIsRejected` covers it.
- **The whole authorization policy is one function**, `setupAuthRoute` in `internal/delivery/http/route/route.go`, so it can be read as a whole. Reads are open to any authenticated user; writes split by data owner (`INVENTARIS` for goods/units/rooms/carriers/suppliers, `CASHIER` for customers); `role` and `user` are `SUPERADMIN`-only including reads. **That split is a starting assumption from the three role names, not a spec** — adjust it as the real division of work emerges.
- **`db/seeder_postgres/004_superadmin.sql` is load-bearing.** `POST /api/v1/user` is `SUPERADMIN`-only, so without a seeded first user the API is locked out of itself. It ships `admin` / `admin12345`, a password committed to this repository — treat it as single-use.
- `middleware.SessionFrom(ctx)` is how a handler gets the caller, and it is the only acceptable source for `created_by`/`updated_by` — the id comes from the verified token, never from the request body. `product_controller.go` is the worked example: the controller reads the session and copies `session.UserID` onto the request DTO's `ActorID` field. Every other slice still writes `NULL`.
- Role names are constants in the `route` package (`RoleSuperadmin`, `RoleCashier`, `RoleInventaris`), matching `db/seeder_postgres/003_role.sql`. They are constants precisely because `role.nama` is renameable through the API and the compiler cannot catch a rename — at least the strings to change are in one place.
- Layering holds: the middleware calls `AuthUseCase.Authenticate` and receives a `*model.Session`. The usecase imports `jwt` but never Fiber.

### Pembelian and the posting engine (migration 000012)

The first transaction document, and the first thing that writes `kartu_stok`. Its number generator and its posting path are built to be reused by sales, transfers, usage, and stock counts — build those on this, not on a master slice.

- **The costing arithmetic is pure and lives apart from the I/O.** `internal/usecase/pembelian_alokasi.go` holds `hitungPosting` and `bagiProporsional`, both operating on `*big.Rat` with no database in sight, and `pembelian_alokasi_test.go` is an **internal** test (`package usecase`) — the only one in the repo. That split is deliberate: this is where a mistake is permanent, so it has to be testable without a fixture.
- **Money and quantities are `math/big.Rat`, never `float64`.** 0.1 has no exact binary representation and these figures become a payable and an inventory valuation. `internal/usecase/numeric.go` parses `NUMERIC` text into exact rationals and rounds once, deliberately, at the end. `formatNumeric` uses `big.Rat.FloatString`, which rounds halves away from zero — the same rule PostgreSQL's `ROUND` applies to `NUMERIC`, and they have to agree or a value computed in Go and one the database recomputes differ by a cent nobody can see.
- **`nilai_masuk` is proportional to `qty_diterima_dasar / qty_dasar`, never the full invoice value.** This is the single most expensive thing in the module to get wrong. `kartu_stok` uses a moving average and outgoing rows lock in the cost in force at the time, so 95 of 100 received at full invoice value prices them at 11.052 instead of 10.500 — and any sale before the rest arrives books that permanently into cost of goods sold. Append-only means it can only be reversed, never repaired.
- Cost per base unit is computed against `qty_dasar`, not `qty_diterima_dasar`. Both numerator and denominator scale by the same ratio so the figure is identical, but expressing it against the invoice quantity is what leaves the remaining value available to a follow-up receipt without recomputing anything.
- **Allocation sums exactly.** `bagiProporsional` rounds each share and then pushes the remainder onto the largest basis, earliest one on a tie. Three lines splitting 100 give 33.33 three times, which is 99.99; the missing cent would silently become inventory value nobody was billed for. The same function does freight, the nota discount, the PPN share, and `bagi-rata-koli`.
- Freight basis is `jumlah_koli`, falling back to `qty_diterima_dasar` when every line is still zero — that fallback is the divide-by-zero guard, not a preference. `ditanggung_supplier` zeroes `biaya_angkut` outright.
- **`biaya_angkut` is not part of `total`.** Total is what the supplier is owed; freight is the carrier's bill and reaches the books through `alokasi_biaya` on the lines. Folding it into total would inflate the payable by money owed elsewhere.
- **Two quantity pairs, and they mean different things.** `qty_faktur`/`qty_dasar` is the paper and drives the payable; `qty_diterima`/`qty_diterima_dasar` is the physical count and drives stock. Omitting `qty_diterima` means it equals `qty_faktur`. Over-delivery is rejected in Go *and* by a CHECK. A difference makes `keterangan_selisih` mandatory — a policy, not a constraint.
- **Every state transition takes a row lock first** (`LockByID`, `SELECT ... FOR UPDATE`), then checks the status, then writes with the status repeated in the `WHERE`. Without the lock two concurrent posting requests both read `DIAJUKAN` and both write `kartu_stok`. `KartuStokRepository.HasRef` is the backstop that does not depend on the status column being right.
- `DRAFT → DIAJUKAN → POSTED → BATAL`, with `DIAJUKAN → DRAFT` on rejection. `INVENTARIS` writes and submits; `SUPERADMIN` posts, rejects, and voids. **This is the only module whose guards split by workflow stage rather than by data owner** — see the comment above those routes in `route.go` for why.
- **A cancellation cannot copy the original cost, and that is the schema's decision, not a shortcut.** `kartu_stok_hitung_saldo` overwrites `nilai_keluar` and `harga_pokok_satuan` on every outgoing row with the balance in force at the time, so an application-supplied cost is discarded. Reversals are therefore valued at the current moving average. Undoing the mixing would need a different valuation method; don't try to work around it by writing to those columns.
- **Document numbers come from `document_counter`**, keyed `(prefix, tahun, bulan)`, taken with one `INSERT ... ON CONFLICT DO UPDATE RETURNING` inside the document's transaction. That statement is atomic and holds the row lock until commit, so concurrent requests queue instead of colliding. A rollback leaves a gap — deliberately: gaps are an annoyance, duplicate numbers are a corruption. Reset is monthly and keyed off the **document's** date, so a July invoice typed in August still gets a July number. Add a prefix constant to `repository` rather than typing a literal.
- `no_faktur_supplier` is unique per supplier via a partial `lower(...)` index excluding `BATAL`, so a mistyped nota can be voided and re-entered. Without a purchase order this document is the only trace of the supplier's invoice, and entering it twice raises stock twice.
- `faktor_konversi` is a snapshot from `product_satuan`, resolved for every line in **one** query (`FindFaktorBatch`, `unnest` of two arrays), not one per line. `qty × faktor` must be a whole number because `qty_dasar` is `BIGINT`.
- Lines are replaced wholesale (`PUT .../detail`), never edited one at a time: they are one thing retyped off one piece of paper, and a partial edit leaves the header's totals disagreeing with its own lines between requests. `DeleteDetail` is safe only on a `DRAFT` — a posted line is what `retur_pembelian_detail` points at.
- `status_penerimaan` follows the `status_pembayaran` rule: **always recomputed, never set from a form.** A cache a form can write is a second source of truth.

### Penerimaan susulan (migration 000013)

The second shipment for goods that did not arrive with the first. It adds stock and never a payable — the supplier's invoice was issued in full with the first delivery and booked in full then, so what is still outstanding after that is goods.

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

One user holds many roles. `user_role` is the only record of that, and permissions are the **union of every role a user holds** — there is no "currently active role".

- **`users.role_active` is gone.** Migration `000002` declared `UNIQUE (role_active)`, which is unique across the whole table rather than per user, so the system could hold exactly one cashier and the second was rejected by the database. Its FK also pointed at `user_role (id)` without `user_id`, so user A's active role could point at user B's grant. Migration `000010` drops the column outright rather than repairing it. Don't reintroduce it; if a "default module on login" preference is ever wanted, that is a UI preference column, not a permission gate.
- **Roles are seeded, then editable.** `SUPERADMIN`, `CASHIER`, `INVENTARIS` come from `db/seeder_postgres/003_role.sql`. `PATCH /api/v1/role/{id}` can rename them, and **renaming a role that authorization code checks by name breaks that code** — nothing in the database can catch it. Retire with `is_aktif = false` and add a new role instead.
- **Grants are managed through the user, not a sub-resource.** `role_ids` on `POST`/`PATCH /api/v1/user` replaces the whole set: absent leaves grants alone, `[]` revokes everything, `[1,3]` ends with exactly those. An explicit `null` is rejected, because `[]` already says "no roles". This is the only place `Optional[[]int64]` is used, and it must stay registered in `config.NewValidator`.
- **`ReplaceRoles` is a diff, not delete-then-insert.** Rows for roles that survive the change are left alone so `user_role.created_at` keeps saying when the grant actually started. It deletes with `role_id <> ALL($2)`, which is why the usecase always passes a **non-nil** slice — a NULL array makes that comparison NULL and deletes nothing, silently turning "revoke all" into a no-op.
- `user_role` is the one table in the codebase where `DELETE` is correct. It is a join table that no transaction table references, so revoking a grant breaks no foreign key and erases no document history — `created_by` on documents points at `users`, not at `user_role`.
- **A roles-only patch still has to move `updated_at`**, which is what `UserRepository.Touch` is for: it writes no other column, fires the `users_set_updated_at` trigger, and yields `sql.ErrNoRows` so an unknown id still answers 404.
- **Role ids are validated before the write**, not left to the foreign key: the FK cannot tell a retired role from a live one, and its message names a constraint rather than the field. `RoleRepository.CountActiveByIDs` compares a count against the number of **deduplicated** ids — pass duplicates and a valid request is wrongly rejected. `repository.IsForeignKeyViolation` (SQLSTATE `23503`) is the race backstop, mapping to a 400.
- **Passwords are bcrypt hashes, hashed in the usecase.** `model.UserResponse` has no password field at all, which is what makes a leak structurally impossible rather than a matter of remembering. bcrypt refuses input over 72 **bytes** while the DTO's `max=72` counts **runes**, so `hashPassword` maps `bcrypt.ErrPasswordTooLong` to `model.Invalid` for the multibyte case.
- Attaching roles to a page of users is **one extra query, not one per user** (`FindRolesByUserIDs` with `= ANY($1)`). `pgx/stdlib` implements `CheckNamedValue`, so a Go `[]int64` passes through `database/sql` untouched — no array wrapper needed.
- The `role_id` list filter is an **`EXISTS`, never a join**. A join to `user_role` returns one row per matching role and silently multiplies the page when a user holds several, breaking both `LIMIT` and `total_item`.
- A user's role list **includes roles retired after being granted** — the grant is still real and still needs revoking. `RoleRef.is_aktif` is what tells them apart. Authorization, when written, must filter on `role.is_aktif` itself rather than assuming every listed role is live.
- `username`, `email`, and `role.nama` are unique **case-insensitively** via `lower(...)` indexes, same as master codes. `email` is nullable, so any number of users may have none.

### Inventory data model (migrations 000002–000008)

`pembelian` and `penerimaan_susulan` now exercise this schema end to end; sales, returns, `pemakaian`, `mutasi`, stock opname, and both payment sides still have **no Go code**. Read these invariants before writing any inventory usecase; several are enforced by the database and will reject wrong code at runtime. `internal/usecase/pembelian_usecase.go` is the worked example of obeying them.

- **`kartu_stok` is the only source of truth for stock and inventory value.** No master table carries a stock column. Never compute stock by summing documents.
- **It is append-only, enforced by trigger.** `UPDATE`, `DELETE`, and `TRUNCATE` all raise. Corrections are new reversing rows that fill `id_kartu_stok_asal`.
- **The trigger computes the balance, not the application.** On insert, `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, and `nilai_akhir` are all overwritten. A usecase supplies only the direction (`stok_masuk` **or** `stok_keluar`, never both), `nilai_masuk`, and the reference columns. Sending a computed balance is silently discarded — don't rely on it.
- **Moving average:** incoming rows shift `harga_pokok_satuan`; outgoing rows never do. Stock reaching zero forces `nilai_akhir` to exactly 0 so rounding residue cannot accumulate.
- Balance is partitioned by `(id_barang, id_ruang)` and ordered by **`id`, not date**. Inserts take a `pg_advisory_xact_lock` on that pair, so concurrent postings for the same product+room serialize.
- The trigger raises on negative stock and on posting into a `periode` with status `TUTUP`. A month with **no** `periode` row counts as open.
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
- ~~posted documents must reject detail edits~~ — done for `pembelian`, via `kunciDenganStatus`. Still owed by every other document type
- ~~cancelling a posted document writes reversing `kartu_stok` rows with `id_kartu_stok_asal` set~~ — done for `pembelian`. **The "HPP copied from the original" half of that rule is not achievable** and was dropped: the trigger overwrites `harga_pokok_satuan` and `nilai_keluar` on every outgoing row, so reversals take the current moving average. See "Pembelian and the posting engine"
- ~~a follow-up receipt must not exceed the outstanding amount (`qty_dasar − qty_diterima_dasar − Σ susulan`)~~ — done, `periksaSisa`, re-checked at posting under the purchase's row lock
- cumulative return qty must not exceed the source document's qty
- allocation must not exceed the payment amount or the document's remaining balance, on either the receivable or the payable side
- cancelled documents must not accept allocations
- credit sales must respect `plafon_kredit`
- a payment, all its allocations, and every touched `status_pembayaran` must be written in one database transaction

The daily reconciliation job over the balance chain (section F) is also not built.

### Adding a module

Follow the `supplier` slice, in this order: migration in `db/migrations_postgres/` (most inventory tables already exist — check first) → `entity` → `model` DTOs → `model/converter` → `repository` (methods take `DBTX`) → `usecase` (validate, own the transaction) → `delivery/http` controller → register in `route.RouteConfig` → wire in `config.Bootstrap` → update `docs/openapi.yaml`.

If the slice writes more than one table, follow `user` instead — it is the worked example of a usecase holding two repositories and committing both tables in one transaction.

If the slice is a **transaction document** — one with a status, a generated number, and stock movements — follow `pembelian`. It is the only one of its kind so far, and copying a master slice for it will leave out the row lock on every transition, the exact-decimal arithmetic, and the reuse of `DocumentCounterRepository` and `KartuStokRepository`.

Master data gets no `DELETE`: every master table is referenced by transaction tables, so deleting a used row either fails on a foreign key or destroys the audit trail. Retire rows with `is_aktif = false` instead. The single exception is `user_role`, for the reasons in "Users and roles".

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
- Tags on an `Optional` field must lead with `omitempty`, and each instantiation must be registered in `config.NewValidator` — otherwise its validation tags are silently ignored. Registered today: `Optional[string]`, `Optional[bool]`, `Optional[[]int64]`, `Optional[int64]`.
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
