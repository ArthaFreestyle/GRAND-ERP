# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Master data is done; **`pembelian` was the first transaction document** and the first writer of `kartu_stok`. **Copy an existing slice when adding a module — don't invent a new shape.**

Implemented: `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `role`, `user`, `unit_kerja`, `ruang` (create/get/list, no patch), `periode` (list/get/tutup/buka), `product` (+ `product_satuan`, `product_harga_jual`), `dokumen`, and the transaction documents `pembelian`, `penerimaan_susulan`, `retur_pembelian`, `pembayaran_utang`, `mutasi`, `pemakaian`, `penjualan`, `penerimaan_pembayaran`, `stok_opname`. Four reads that are **not** modules: `riwayat_beli`, `utang_supplier`, `stok_per_ruang`, `piutang_pelanggan`.

**Only `retur_penjualan` still has no Go layer** (its tables exist in migrations `000002`–`000008`). The posting engine itself is finished — every remaining shape has a template.

### Pick a template by shape, not by domain

| Shape | Template | What it adds |
| --- | --- | --- |
| plain master slice | `supplier` | nullable unique `kode`, PATCH presence, `LEFT JOIN` in list |
| slice writing two tables | `user` | two repos, one tx; `Optional[[]GrantRequest]`, bcrypt, `Touch` |
| transaction document | `pembelian` | state machine, generated number, `big.Rat`, `kartu_stok` |
| derives from a POSTED document | `penerimaan_susulan` / `retur_pembelian` | parent row lock, copied cost snapshot, quota re-checked at posting, per-document unique index on the source line. Mirror pair — read side by side; only direction differs |
| money, no stock | `pembayaran_utang` / `penerimaan_pembayaran` | no approval state, caches recomputed not reversed. Mirror pair |
| stock in two directions | `mutasi` | incoming valued from outgoing `RETURNING`; canonical lock ordering; no approval stage |
| stock out, no counterparty | `pemakaian` | extra `DISETUJUI` stage deciding a *quantity*; terminal rejection |
| stock out to an outsider, money owed | `penjualan` | first receivable, credit limit at posting, `status_pembayaran` for a type with no allocation |
| stock to/from nowhere | `stok_opname` | selisih vs a frozen snapshot; freezes its `ruang` against every other module |
| read that is not a module | `riwayat_beli` | no table, no migration, query in the owning repository |
| keyed on a natural key | `periode` | routes on the key, synthetic answer for a keyless row, cross-cutting refusal via trigger |
| store outside PostgreSQL | `dokumen` | storage interface, file/row ordering, worker reconciliation |

Do **not** model a transaction document on a master slice; the concerns barely overlap.

### Facts

- Module path `Arthafreestyle/ERP` (no domain prefix); internal imports `Arthafreestyle/ERP/internal/...`
- Go 1.25.0 — required by Fiber v3.4.0, which refuses to build on 1.24
- Auth: bearer JWT, role guards per route, active session context. See "Authentication and authorization" and "Unit kerja (isu #12)"
- `cmd/worker` has one job, the orphan-attachment sweep. Scheduler is a `time.Ticker` per job in `internal/config/worker.go`, wired by `BootstrapWorker` — the counterpart of `Bootstrap`, so "wiring happens only in `internal/config`" still holds
- Not built: captcha (Redis wired but unused), logout/refresh, session revocation (stateless tokens cannot be revoked), the daily reconciliation job over the balance chain
- `product` and `pembelian` are the only modules that fill `created_by`/`updated_by` from `middleware.SessionFrom`. Every other slice writes `NULL` — the plumbing exists, they just don't use it

## Stack

- Go + [Fiber v3](https://github.com/gofiber/fiber)
- PostgreSQL via `database/sql` + `jackc/pgx/v5` (through `pgx/v5/stdlib`) — **no ORM**; write SQL by hand
- Redis — captcha sessions with TTL
- viper + logrus; migrations by [golang-migrate](https://github.com/golang-migrate/migrate)

## Commands

```bash
go run ./cmd/web                 # HTTP server
go run ./cmd/worker              # background worker
go build ./... && go vet ./...
go test ./...                                        # all
go test ./internal/usecase -run TestSatuanCreate     # one test (regex)
go test -v -race ./...
```

**`gofmt -l .` is not a usable signal on a Windows checkout.** `core.autocrlf=true` writes CRLF into the working tree while the index holds LF; gofmt wants LF, so it lists ~74 of ~90 files. Nothing is misformatted (`git ls-files --eol` shows `i/lf`). **Do not `gofmt -w .`** — check individual files you touched, or add `.gitattributes` with `*.go text eol=lf` and re-checkout.

### Docker

```bash
docker compose up -d --build     # postgres, redis, migrations, seeders, web, worker — in that enforced order
docker compose up -d --build web # rebuild after a Go change
docker compose down -v           # stop and discard data (re-triggers docker/initdb/)
```

- **PostgreSQL is published on host port `5433`**, not 5432 (a local install usually owns 5432). Inside the compose network it is still `postgres:5432`.
- **The `seed` service lists each seeder file by name** rather than globbing. A new seeder not added to that list never runs under Docker.

### Tests

Compose also creates and migrates `grand_erp_test`:

```bash
docker compose up -d
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5433/grand_erp_test?sslmode=disable'
go test ./...
```

- Tests in `internal/usecase` run against a **real PostgreSQL** and skip themselves without `TEST_DATABASE_URL`. **A green `go test ./...` without it means skipped, not passed** — run `-v` when you need to know. Migrate the scratch DB first; the tests clear tables but do not create the schema. Everything outside `internal/usecase` is a pure unit test.
- What they assert lives in the database (pagination stability under duplicate names, `ILIKE` escaping, many rows sharing `kode = NULL`, `NUMERIC` round-tripping), where a mock would just agree with a wrong query.
- Every test starts with `newApp(t)` (= `requireDB` + `truncateMaster` + the same graph `config.Bootstrap` wires, minus Fiber). **A new usecase needs a field on that `app` struct** or its tests have nothing to call.
- `truncateMaster` (`internal/usecase/main_test.go`) deletes in dependency order: `stok_opname*` **first of all** — they point AT `kartu_stok`, the reverse of every other table — then `kartu_stok`, then documents pointing at `pembelian_detail` (`penerimaan_susulan*`, `retur_pembelian*`) and at `pembelian` (`pembayaran_utang*`), then `mutasi*`, the purchase tables, `dokumen`, master tables, `user_role`, `users`, `role`. Add new tables on the correct side — `users` comes after anything whose `created_by` references it, and `periode` before `users`.
- It uses `DELETE`, not `TRUNCATE` (which would cascade into `kartu_stok`, whose guard trigger raises). `kartu_stok` refuses `DELETE` too, so `truncateMaster` disables `kartu_stok_append_only` for the wipe and re-enables it in a `defer`. **That licence is the scratch database's alone** — reaching for it elsewhere defeats the guarantee the whole valuation rests on.

**Which file a test goes in follows the shape of what it pins, not the module it calls.** A module's own behaviour → `<module>_usecase_test.go`. A read that is not a module → a file named after the read (`riwayat_beli_test.go`). **One behaviour repeated across several modules → a single file spanning them** (`fase6_read_scope_test.go`, `ruang_unit_scope_test.go`, `master_data_test.go`) — reading the repetitions together is what makes a deliberate exception legible as a decision rather than an omission. Anything needing an **unexported** symbol → `package usecase` (`pembelian_alokasi_test.go`, `harga_jual_test.go`, `auth_token_test.go`); everything else is `package usecase_test`.

### Config

Comes from `config.json` in the working directory; **any key is overridable by an env var** with `.` → `_` (`DATABASE_HOST`, `WEB_PORT`). `config.json` is gitignored; the tracked file is `config.example.json`.

`NewViper` **panics** when `config.json` is missing — env vars alone cannot boot. The Dockerfile copies `config.example.json` to `config.json` in the image; compose overrides the environment-dependent keys. `.dockerignore` excludes the real `config.json`.

**A new config key has up to four homes:** `config.example.json` (always), the `web`/`worker` `environment:` blocks in `docker-compose.yml` (if a container needs a non-default), and `.env.example` — the only list of what a deployer may set, since every compose entry is `${VAR:-default}`. Keep `.env.example`'s two groups apart: `POSTGRES_*` configures the PostgreSQL image, `DATABASE_*`/`REDIS_*` are read by the app containers (viper maps `database.host` → `DATABASE_HOST`); compose derives the second from the first.

`jwt.secret` is the exception: present in the example but **empty**, because a shipped signing key is worse than a missing one. Compose supplies a labelled dev value.

### Migrations

CLI **must be built with the postgres driver tag** or it fails with `unknown driver postgres`:

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate -path db/migrations_postgres -database "$DSN" up
migrate -path db/migrations_postgres -database "$DSN" down 1
migrate -path db/migrations_postgres -database "$DSN" force <version>   # clear dirty state
migrate create -ext sql -dir db/migrations_postgres -seq <name>
```

Migration `000001` creates only the shared `set_updated_at()` trigger function — every table with `updated_at` reuses it.

Seeders live in `db/seeder_postgres/` (`001_ruang`, `002_satuan`, `003_role`, `004_superadmin`), applied separately, idempotent (`ON CONFLICT DO NOTHING`). **Their conflict target must name the index expression** — `ON CONFLICT (lower(kode))`, not `(kode)` — since migration `000009` moved master uniqueness onto `lower(...)`.

## Architecture

Layered, one-directional. A layer may only import the layers below it:

```
delivery  →  usecase  →  repository  →  PostgreSQL / Redis / upstream HTTP
              ↑ ↑
           model    entity
```

| Package | Role | Rules |
| --- | --- | --- |
| `cmd/web`, `cmd/worker` | entrypoints | wire config → db/redis → repository → usecase → delivery |
| `internal/config` | viper, logrus, postgres, redis, fiber | every dependency built here and injected downward; nothing else reads env |
| `internal/entity` | domain structs mapped to tables | no JSON tags, no framework imports |
| `internal/model` | request/response DTOs + converters | entity ⇄ model conversion lives here, never in handlers |
| `internal/repository` | data access | all SQL; takes `context.Context` and a `DBTX` so usecases compose transactions |
| `internal/usecase` | business logic, validation, transaction boundaries | depends on repository interfaces, returns models — never Fiber types |
| `internal/delivery` | Fiber v3 handlers + routing | bind → call usecase → respond; no business logic, no SQL |

- **Handlers must not touch `database/sql`.** A query belongs in a repository even if used once.
- **Transactions are owned by the usecase layer.** Repositories take an executor argument.
- **`internal/entity` never leaks past `internal/usecase`.**
- **Wiring happens only in `config.Bootstrap`/`BootstrapWorker`.**

### Errors and responses

One envelope, `model.WebResponse[T]`, one error path:

- Usecases return `model.Invalid/NotFound/Conflict/Unauthorized/Forbidden(...)` — semantic kinds from `internal/model/errors.go`, **not** HTTP codes and **not** `fiber.NewError`. That is what keeps Fiber out of the usecase layer.
- `statusForKind` (`internal/config/fiber.go`) is the single place a kind becomes a status code. Handlers just `return err`; the Fiber `ErrorHandler` formats it, unwraps `validator.ValidationErrors` into `validation_errors`, and logs only 5xx. A bare `error` becomes a 500 with a generic message.
- **Don't hand-roll driver-error mapping.** `internal/usecase/shared.go` holds the funnels every slice wraps repository calls in: `notFoundOnNoRows` (404), `conflictOnUnique` (`23505` → 409), `invalidOnForeignKey` (`23503` → 400), `conflictOnExclusion` (`23P01` → 409), `invalidOnCheck` (`23514` → 400), `conflictOnTransisi` (`repository.ErrTransisiStatus` → 409), `conflictOnRuangBeku` (`55000` → 409). SQLSTATE predicates live in `internal/repository/pgerror.go`. `pageMetadata` is there too and must run *after* `PageRequest.Normalize` or `total_page` divides by zero.
- `invalidOnCheck` exists mainly for the `kartu_stok` trigger, which raises `check_violation` for negative stock and for a closed `periode`. A trigger's `RAISE` carries no constraint name, so each call site supplies its own message and the database's text never reaches the client.

Controller boilerplate is fixed — copy it verbatim; these strings are part of the contract:

- `ctx.Bind().Body(...)` failure → `model.Invalid("malformed request body")`; `.Query(...)` → `"malformed query parameters"`; `strconv.ParseInt(ctx.Params("id"), ...)` → `"id must be an integer"`.
- Create answers `fiber.StatusCreated`; everything else 200 via a bare `ctx.JSON`.
- **`WebResponse.Data` deliberately has no `omitempty`.** An empty slice is "empty" to `encoding/json`, so omitempty would drop the key on exactly the page with no rows and break a client reading `data.length`. The cost is `"data": null` on errors; keep it.
- Pagination defaults are `PageRequest.Normalize` (page 1, size 20, capped at 100). The usecase calls it; the controller does not.

### Authentication and authorization

Bearer JWT, stateless by decision. Every `/api/v1` route needs a token except `POST /api/v1/auth/login`.

- **Route guards must be the FIRST handler argument.** Fiber v3 runs `Get(path, handler, handlers...)` in the order given, so `Get(path, controller, guard)` puts the guard after a controller that never calls `Next()` — the table looks protected and protects nothing. Write `Get(path, guard, controller)`. `TestRouteGuardsRunBeforeHandler` pins both halves, including Fiber's ordering.
- **Tokens cannot be revoked.** Nothing is stored server-side; `is_aktif = false` or a revoked role does not reach an issued token. `jwt.ttl_minutes` (default 60) is the entire bound. **Do not "fix" this with a Redis blacklist** — it reinstates the per-request lookup JWT was chosen to avoid. `switch-context` mints a new token and cannot touch the old one.
- **Grants live in the token claims**, so authorization touches no database. A grant made or revoked takes effect at next login or `switch-context`. Only *usable* grants are embedded (role `is_aktif`, and the unit `is_aktif` when scoped) — that is where retired-does-not-authorize is enforced; `FindRolesByUserIDs` returns retired grants on purpose, for the management view.
- **`jwt.secret` has no default and the process refuses to start without one** (`config.NewAuthConfig`, min 32 chars). A baked-in default is a key every deployment shares; a random per-process key breaks every restart and every multi-instance deploy, both silently.
- **Login answers one message for every failure** (unknown username, wrong password, disabled account) — distinguishing them enumerates usernames. The unknown-username path runs a dummy bcrypt compare so it does not return measurably faster.
- `Authenticate` pins the signing method via `jwt.WithValidMethods`; without it the parser trusts the token's own `alg` and accepts `alg=none` (`TestAlgNoneTokenIsRejected`).
- **The whole authorization policy is one function**, `setupAuthRoute` in `route.go`, so it reads as a whole. Reads open to any authenticated user; writes split by data owner (`INVENTARIS` for goods/units/rooms/carriers/suppliers, `CASHIER` for customers); `role` and `user` are `SUPERADMIN`-only including reads; **every document writing `kartu_stok` splits by workflow stage instead** (`INVENTARIS` types and submits, `SUPERADMIN` posts/rejects/voids) — except `mutasi` and `penjualan`, which have no approval stage, so the split lives in the route table alone. `dokumen` has no split at all — attachments belong to no module, and state protects them. **That split is a starting assumption from three role names, not a spec** — adjust as the real division of work emerges.
- **`db/seeder_postgres/004_superadmin.sql` is load-bearing.** `POST /api/v1/user` is `SUPERADMIN`-only, so without a seeded first user the API is locked out of itself. It ships `admin` / `admin12345` — a password committed to this repository; treat it as single-use.
- `middleware.SessionFrom(ctx)` is the only acceptable source for `created_by`/`updated_by` — the id comes from the verified token, never the body. `product_controller.go` is the worked example.
- Role names are constants in the `route` package (`RoleSuperadmin`, `RoleCashier`, `RoleInventaris`) matching `003_role.sql` — constants precisely because `role.nama` is renameable through the API and the compiler cannot catch a rename.

## Modules

### Pembelian and the posting engine (migration 000012)

The first transaction document and the first writer of `kartu_stok`. Its number generator and posting path are built for reuse — build new documents on this, not on a master slice.

- **The costing arithmetic is pure and lives apart from the I/O.** `internal/usecase/pembelian_alokasi.go` holds `hitungPosting` and `bagiProporsional` on `*big.Rat` with no database in sight; its test is **internal** (`package usecase`). This is where a mistake is permanent, so it has to be testable without a fixture.
- **Money and quantities are `math/big.Rat`, never `float64`.** `internal/usecase/numeric.go` parses `NUMERIC` text into exact rationals and rounds once, at the end. `formatNumeric` uses `big.Rat.FloatString`, which rounds halves away from zero — the same rule PostgreSQL's `NUMERIC` `ROUND` applies, and they must agree or a value computed in Go and one recomputed in SQL differ by a cent nobody can see.
- **`nilai_masuk` is proportional to `qty_diterima_dasar / qty_dasar`, never the full invoice value.** The single most expensive thing here to get wrong: `kartu_stok` uses a moving average and outgoing rows lock in the cost in force at the time, so 95 of 100 received at full invoice value prices them at 11.052 instead of 10.500 — and any sale before the rest arrives books that permanently into COGS. Append-only means it can only be reversed, never repaired.
- Cost per base unit is computed against `qty_dasar`, not `qty_diterima_dasar` — identical figure (both scale by the same ratio), but expressing it against the invoice quantity leaves the remaining value available to a follow-up receipt without recomputation.
- **Allocation sums exactly.** `bagiProporsional` rounds each share, then pushes the remainder onto the largest basis, earliest on a tie. Three lines splitting 100 give 33.33 × 3 = 99.99; the missing cent would silently become inventory value nobody was billed for. Same function does freight, nota discount, PPN share, and `bagi-rata-koli`.
- Freight basis is `jumlah_koli`, falling back to `qty_diterima_dasar` when every line is zero — that fallback is the divide-by-zero guard, not a preference. `ditanggung_supplier` zeroes `biaya_angkut`.
- **`biaya_angkut` is not part of `total`.** Total is what the supplier is owed; freight is the carrier's bill and reaches the books through `alokasi_biaya` on the lines.
- **Two quantity pairs, meaning different things.** `qty_faktur`/`qty_dasar` is the paper and drives the payable; `qty_diterima`/`qty_diterima_dasar` is the physical count and drives stock. Omitting `qty_diterima` means it equals `qty_faktur`. Over-delivery is rejected in Go *and* by a CHECK. A difference makes `keterangan_selisih` mandatory — a policy, not a constraint.
- **Every state transition takes a row lock first** (`LockByID`, `SELECT ... FOR UPDATE`), then checks the status, then writes with the status repeated in the `WHERE`. Without the lock two concurrent posts both read `DIAJUKAN` and both write `kartu_stok`. `KartuStokRepository.HasRef` is the backstop that does not depend on the status column being right.
- `DRAFT → DIAJUKAN → POSTED → BATAL`, with `DIAJUKAN → DRAFT` on rejection.
- **A cancellation cannot copy the original cost** — `kartu_stok_hitung_saldo` overwrites `nilai_keluar` and `harga_pokok_satuan` on every outgoing row, so an application-supplied cost is discarded and reversals are valued at the current moving average. Undoing the mixing would need a different valuation method; don't work around it by writing to those columns.
- **Document numbers come from `document_counter`**, keyed `(prefix, tahun, bulan)`, one `INSERT ... ON CONFLICT DO UPDATE RETURNING` inside the document's transaction — atomic, holding the row lock until commit, so concurrent requests queue. A rollback leaves a gap, deliberately: gaps annoy, duplicate numbers corrupt. Reset is monthly off the **document's** date, so a July invoice typed in August gets a July number. Add a prefix constant to `repository` rather than a literal.
- `no_faktur_supplier` is unique per supplier via a partial `lower(...)` index excluding `BATAL`, so a mistyped nota can be voided and re-entered. Without a purchase order this document is the only trace of the supplier's invoice, and entering it twice raises stock twice.
- `faktor_konversi` is a snapshot from `product_satuan`, resolved for every line in **one** query (`FindFaktorBatch`, `unnest` of two arrays). `qty × faktor` must be a whole number because `qty_dasar` is `BIGINT`.
- Lines are replaced wholesale (`PUT .../detail`), never edited one at a time: they are one piece of paper retyped, and a partial edit leaves the header's totals disagreeing with its own lines. `DeleteDetail` is safe only on a `DRAFT` — a posted line is what `retur_pembelian_detail` points at.
- `status_penerimaan` follows the `status_pembayaran` rule: **always recomputed, never set from a form.**

### Penerimaan susulan (migration 000013)

The second shipment for goods that did not arrive with the first. Adds stock, never a payable — the supplier's invoice was issued and booked in full with the first delivery.

- **Why a separate document rather than raising `qty_diterima`.** A POSTED `pembelian` must not change and `kartu_stok` is append-only: editing the posted line would break document immutability, make cancellation unauditable (which rows get reversed?), and erase when the goods actually turned up.
- **`harga_pokok_satuan_dasar` is copied from the source line**, never recomputed and never read off the current moving average. That is what makes a purchase and all its follow-ups contribute exactly the invoice value to inventory, and why the average does not shift when the remainder arrives. **Copying a cost rather than deriving one is the pattern every derived document follows.**
- **Two snapshots from different places, deliberately.** `faktor_konversi` is resolved from `product_satuan` *now* (a fresh count may arrive in a different unit than the invoice used); the cost is copied from the source. Quantity conversion follows current master; cost follows the invoice.
- **The quota check that counts runs at posting, under `PembelianRepository.LockByID`.** The one in `siapkanDetail` only produces a friendlier error sooner. Two drafts may both claim the same remainder — a draft is not a delivery.
- Only **POSTED** follow-ups count toward the outstanding figure. `pembelianDetailSusulan` (`pembelian_repository.go`) is that predicate, declared once and reused by `FindDetail`, `FindDetailByID`, and `RecalculateStatusPenerimaan`.
- **`pembelian.status_penerimaan` is rewritten from here** by `RecalculateStatusPenerimaan` after posting and voiding. The purchase is POSTED by then and can no longer recompute its own cache.
- **Three derived quantities on a purchase line answering different questions:** `selisih_dasar` (what the first delivery was short, frozen once posted), `qty_susulan_dasar` (what turned up later), `sisa_dasar` (what is still owed). Don't collapse them.
- `penerimaan_susulan_detail_baris_uidx` stops one source line appearing twice in one document — without it two lines each pass the quota check alone and together exceed it. The usecase catches it first so the message names the field.
- **Cancelling a purchase is refused while a POSTED follow-up exists** (`HasPostedSusulan` → 409): the purchase reverses only rows it wrote itself, so the follow-up's stock would survive unexplained and could not be reversed afterwards either.
- `jenis_transaksi` gained `'PENERIMAAN_SUSULAN'`. **`ALTER TYPE ... ADD VALUE` cannot be undone** — no `DROP VALUE` exists, and removing one means rebuilding the type and every column using it, including append-only `kartu_stok`. The down migration says so and leaves the value behind. `ADD VALUE IF NOT EXISTS` keeps the up idempotent. Safe inside golang-migrate's transaction on PG 12+ as long as the value is not *used* in the same transaction.
- No freight columns. Value entering stock is the remaining share of the original invoice value, so one invoice contributes exactly its own value however many times goods turn up.
- `id_supplier` and `id_ruang` are copied from the purchase, not chosen. Goods needing to move rooms after arrival are a `mutasi`.

### Retur pembelian (migration 000014)

Goods sent back to the supplier. The mirror of `penerimaan_susulan`: same parent, same copied cost, same quota-on-a-line shape, goods the other way. What follows is only what the direction changes.

- The tables predate this (migration `000005`); `000014` adds the approval flow, cancellation columns, status CHECK, a `(tanggal DESC, id DESC)` index, and the per-document unique index. **No `ALTER TYPE`** — `'RETUR_PEMBELIAN'` has been in `jenis_transaksi` since `000002`.
- **What may be returned is what actually arrived**: `qty_diterima_dasar + Σ POSTED susulan − Σ POSTED retur`. Note what is absent: `qty_dasar`. The invoice quantity is what the supplier billed, and goods that never turned up cannot be sent back — a shortfall is chased with a `penerimaan_susulan`. `pembelianDetailRetur` is that sum.
- **This is a different axis from the outstanding quantity, and mixing them is the mistake to avoid.** Goods returned were still received, so `status_penerimaan` is **never** recomputed from here. `sisa_dasar` and `qty_dapat_diretur` can both be nonzero on the same line. Copying `penerimaan_susulan` wholesale would wrongly add the call.
- **`total` and what `kartu_stok` records leaving are different numbers, on purpose.** `total` is the invoice cost summed from `harga_pokok_satuan_dasar` copied off the source lines — that copy is what makes a purchase and its return cancel out. `kartu_stok` values every outgoing row at the moving average in force, because those goods were blended into older stock on arrival. Neither figure can be made to be the other.
- **A return reduces the payable, but never by its own `total`** — `total` carries the freight share and PPN treatment, while `pembelian.total` excludes freight entirely, so subtracting one from the other over-credits the supplier. Uses `nilai_kredit_utang` instead (see "Pembayaran utang"). Posting and voiding both call `RecalculateStatusPembayaran`.
- **Cancelling a purchase is refused while a POSTED return exists** (`HasPostedRetur` → 409): the purchase's reversal covers the full received quantity while the return already took part of it out, driving the balance negative — and the return would be left pointing at a `BATAL` purchase that already accounted for those goods.
- **Voiding a `penerimaan_susulan` whose goods have been returned is refused by the trigger**, not by Go — the balance is computed inside it under an advisory lock precisely so no reader decides it first. Surfaces as `invalidOnCheck` → 400.
- **The negative-stock guard on this module's own posting is defensive**, but stopped being purely theoretical once `mutasi`/`pemakaian` could draw a room down independently — which is why `invalidOnCheck` was wired in from the start.
- `alasan` is nullable in the schema but **required by the usecase**, and a patch may not clear it — the only record of why goods already paid for went back. A policy, not a constraint (the `keterangan_selisih` precedent).
- Cancellation appends reversing rows with `NilaiMasuk = asal.NilaiKeluar` — what the ledger recorded leaving is the one figure that makes the pair sum to zero. An outgoing row's own `NilaiMasuk` is sent as an explicit `"0"` (the column is NOT NULL and `""` is not a NUMERIC).
- `qty_retur_dasar`/`qty_dapat_diretur` ride on `PembelianDetailResponse`, so the return-entry screen needs no endpoint of its own — which changed `pembelianDetailReadColumns` and the scan order in `FindDetail` and `FindDetailByID`.
- Prefix `RB`.

### Pembayaran utang and the payable side (migration 000015)

Money paid to suppliers. Tables from `000008`; `000015` adds value guards, giro CHECKs, the per-document unique index, and `retur_pembelian.nilai_kredit_utang`.

- **It touches no stock, and that absence shapes everything.** `DRAFT → POSTED → BATAL`, with **no `DIAJUKAN`** — do not add one to match `pembelian`. The approval stage exists there because `kartu_stok` is append-only: a wrong posting can only be reversed, at a moving average that has since shifted. An allocation has no such residue; it can be voided and every cache recomputed exactly. The two-person control survives as the route split (`CASHIER` prepares, `SUPERADMIN` releases), one state fewer.
- **Three rules, deliberately not symmetric.** A payment may be allocated at most up to its own amount, and **less is normal** — the remainder is a credit sitting with the supplier, which is why `alokasi` may be empty on create. An invoice may receive at most what it still owes. **Never force allocation to balance.**
- **An uncashed giro is not a payment** — the trap in this module. Posting a `BELUM_CAIR` giro freezes its allocations and closes the document while leaving every payable where it was; `Cairkan` moves `status_pembayaran`. So **the remaining-balance check runs again at clearing**, not just at posting — a cash payment can settle the same invoice in between. `TolakGiro` gives nothing back, because nothing was taken.
- **`nilai_kredit_utang` is not `retur_pembelian.total`.** Cost carries freight the supplier never received. The credit is `pembelian.total × nilai_faktur_retur / pembelian.subtotal`, where `nilai_faktur_retur` sums `subtotal / qty_dasar × qty_retur_dasar` over the source lines. **Scaled against `total` rather than taken raw**, because `total` already carries the nota discount, PPN, and rounding line — crediting raw line values for a full return over-credits by exactly the nota discount. The scaling makes the invariant checkable: returns never credit more than `total`, and credit exactly `total` when everything goes back.
- **The credit is frozen at posting, not computed on read.** It derives from two POSTED documents that can no longer change, so recomputing always gives the same answer — until one day it doesn't, and an old payable silently changes value. **Every money figure in this project is a snapshot.**
- **`status_pembayaran` is a cache: always recomputed, never set from a form.** `RecalculateStatusPembayaran` is one statement, so there is no window where the cache disagrees with the rows it summarises, and **everything that can change the answer calls it** — posting/voiding a payment, clearing/rejecting a giro, posting/voiding a return. Two fragments back it, declared once in `pembelian_repository.go`: `pembelianAlokasiEfektif` (POSTED payments; for giro only `CAIR`) and `pembelianKreditRetur`.
- `SEBAGIAN` covers a return-only credit with no money paid — correct, and both figures ride on the response so a screen can say which one did it.
- **Cancelling a purchase is refused once it has been paid**, including while an uncashed giro points at it — that giro has not reduced the payable, but it is a document circulating against the invoice.
- `id_supplier` and `metode` are **absent from the update DTO**: changing the first leaves every allocation pointing at another supplier's invoices; the second decides whether the giro columns may be filled at all. Change either by cancelling and re-entering — which is also what the bank statement will show.
- `GET /supplier/{id}/utang` is a **read that is not a module** (query in `pembelian_repository.go`, `SupplierUseCase` borrows `PembelianRepository`). Ordered **oldest first**, unlike every other list, because it is a queue to work through.
- Prefix `PU`.

### Penerimaan pembayaran and the receivable side (migration 000024, isu #20)

Money received from customers — the mirror of `pembayaran_utang`. **Read that module first whenever in doubt here.** Tables exist since `000006`; `000024` only adds the value guards. **No `ALTER TYPE`**; writes no `kartu_stok`, so it touches no trigger, no periode, no ruang freeze.

- Second module touching no stock; every consequence `pembayaran_utang` draws from that applies unchanged — no `DIAJUKAN`, two-person control in the route split, caches recomputed from one statement. `DRAFT → POSTED → BATAL`.
- Same three rules, same uncashed-giro trap: `CairkanGiro` is what moves `status_pembayaran`, and the remaining-balance check runs again at clearing.
- **A `TUNAI` nota may never receive an allocation — no counterpart on the payable side to copy.** `pembelian` has no cash/credit split; every purchase behaves like a `KREDIT` one until paid. `periksaSisaPiutang` refuses a `TUNAI` nota **by name** (`"penjualan ini TUNAI, tidak pernah jadi piutang"`), not with "sisa piutang habis" — it never had a balance, since `RecalculateStatusPembayaran` marks it `LUNAS` the instant it posts. This is also why `penjualanAlokasiEfektif` never has to special-case `jenis_pembayaran`: nothing can allocate against a `TUNAI` nota, so the predicate only ever sees `KREDIT` rows.
- **`penjualanKreditRetur` is a named zero (`0::NUMERIC(20, 2)`), not a bare literal.** The payable side needed `nilai_kredit_utang` as a column because harga pokok carries freight the supplier never received; `retur_penjualan_detail` has carried both `harga_satuan_input` and `hpp_satuan_dasar` since `000006`, so a sales return's credit will most likely just be its own total. Wired into `RecalculateStatusPembayaran`, `PiutangBerjalan`, and `FindPiutangPelanggan`, so the day `retur_penjualan` exists **one fragment changes and none of the three callers do.**
- **`PenjualanRepository.RecalculateStatusPembayaran` is a full cache now.** `TUNAI` → `LUNAS` unconditionally (only ever called for a `TUNAI` nota once, at `Posting`, before the header's status flips). `KREDIT` → `BELUM`/`SEBAGIAN`/`LUNAS` from `penjualanAlokasiEfektif + penjualanKreditRetur` against `p.total`. Four callers, same count as the payable side.
- **`PiutangBerjalan` and `FindPiutangPelanggan` both gained the same subtraction**, closing a ratchet: before this module `PiutangBerjalan` summed raw `total` with nothing to reduce it, so `plafon_kredit` could only ever be consumed, never freed. `FindPiutangPelanggan`'s response shape is unchanged — only what feeds `sisa_piutang` got real.
- `FindSisaPiutang` mirrors `FindSisaUtang`, called only after `LockByID` — the lock is what stops two payments both reading the same remaining balance. It additionally returns `JenisPembayaran`, which `SisaUtang` has no counterpart for.
- `HasPostedAlokasi` is wired into `PenjualanUseCase.Batal` the way the payable one is into `PembelianUseCase.Batal`.
- **The `plafon_kredit` race is still not closed**, and isu #20 says so explicitly rather than implying otherwise. Two `KREDIT` notas posted at the same moment can both pass `periksaPlafon`; the fix would be an advisory lock keyed on `id_pelanggan`, the shape `KunciSaldo` uses for `(id_barang, id_ruang)`.
- Borrows only `PenjualanRepository`, never `PelangganRepository` — the shape `PembayaranUtangUseCase` has with `PembelianRepository`. `id_pelanggan` is validated by the foreign key alone.
- Prefix `PP`.

### Mutasi antar ruang (migration 000018)

Goods moving between rooms — the first document writing `kartu_stok` **in both directions at once**. Tables from `000007`, enum values since `000002`, so **no `ALTER TYPE`**; `000018` adds the status CHECK, cancellation columns, and indexes.

- **One `mutasi_detail` line is two `kartu_stok` rows, in one transaction.** Splitting it into two documents would let goods leave the warehouse without entering the shop, with nothing holding the halves together. Goods appearing with no origin are not a mutasi at all — that is `stok_opname`.
- **The incoming row is valued at exactly `nilai_keluar` from the outgoing row, read back from `RETURNING`.** The module's whole correctness argument. Cost follows the source room, or moving goods changes the value of inventory — but the application *cannot compute that cost*: the source room's moving average is known only to `kartu_stok_hitung_saldo` inside its advisory lock, and the trigger overwrites `nilai_keluar`/`harga_pokok_satuan` anyway. So the order of the two inserts is forced.
- **`mutasi_detail.harga_pokok_satuan_dasar` is written at posting, not at draft** (the column is nullable for that). A cost typed on a draft is the average at draft time — a different number, wrong in a way nobody would notice.
- **No `DIAJUKAN`, for a different reason than `pembayaran_utang`'s.** Mutasi's mistake is cheap: goods recorded in the wrong room, while **total stock and total inventory value do not move at all** — no outside party, no money, and the correction is another mutasi the same person may write. `pembayaran_utang` drops the stage because voiding leaves no residue; mutasi's cancellation *does* leave residue. **The justification is the size of the bet, not the tidiness of the undo.**
- **Dropping `DIAJUKAN` moves the two-person control into the route table**: `INVENTARIS` reaches `DRAFT`, `SUPERADMIN` posts and voids. The cost is that there is no "this draft is ready" signal, so the list endpoint **must** be able to filter `status=DRAFT`, and `terlama_dulu` orders it oldest first like `GET /supplier/{id}/utang`. If that is not enough, add `DIAJUKAN` — adding a value to `mutasi_status_check` is far cheaper than removing one would have been.
- **Two advisory locks in one transaction, which is a real ABBA.** The trigger locks per `(id_barang, id_ruang)` and a mutasi takes two for the same product, ordered by direction — so two opposite transfers deadlock. `KartuStokRepository.KunciSaldo` takes them all up front in canonical order; mapping `40P01` to a 409 and asking the client to retry would hand a real defect to whoever is at the counter. `TestMutasiBerlawananArahTidakDeadlock` fails with `deadlock detected` without it.
- **The periode lock is taken first, uniformly**, because that is the order the trigger takes them. A writer pre-locking balances without it opens a different cycle: a closing queued for the exclusive periode lock, a posting holding it shared and waiting on our balance lock, and us queued behind the closing. `PeriodeRepository.LockShared` exists only for that.
- **Cancellation is not symmetric in value and cannot be made so** — the row leaving the destination room is valued at that room's current average. So a transfer and its void always cancel in quantity, not always in value. It can also be **refused outright** if the goods have since left the destination room; the remedy is another mutasi.
- **The same product may appear on two lines**, unlike the quota-on-a-parent-line modules: here the quota is the source room's balance, the usecase sums lines per product before checking, and the trigger checks every insert. Two lines in different input units are a legitimate way to type a transfer.
- **Both rooms may change while `DRAFT`** (no `mutasi_detail` row names a room). The `id_ruang_asal <> id_ruang_tujuan` check runs against the **stored** row, not the patch: moving only one of the two can collide with the other already there.
- `periksaSaldo` is for the message, not the guard — it runs *after* `KunciSaldo`, which is what makes the figure it reports true for the rest of the transaction.
- No money column anywhere. Prefix `MT`.

### Pemakaian internal (migration 000021, isu #9)

Goods leaving for internal use — no nota, no return, **no counterparty at all**. Tables and enum values predate it, so **no `ALTER TYPE`**; `000021` aligns the status vocabulary and adds the list index.

- **What is posted is `qty_disetujui_dasar`, never `qty_dasar` — the most expensive mistake here.** `qty_dasar` is what was asked for, frozen when the line is typed and never touched, not even by approval. `qty_disetujui_dasar` is nil until `Setujui` and is the only thing `Posting` reads; 0 means that line was refused on its own. Filling it at `Create` would let a requester approve their own request through the back door.
- **A line whose approved quantity is nil or zero is skipped at posting**, never inserted as a row moving nothing. If every line is zero, posting is refused (400) — that is a rejection in substance, not an internal usage.
- `DRAFT → DIAJUKAN → DISETUJUI → POSTED → BATAL`, with `DIAJUKAN → DITOLAK` as a branch. **`DITOLAK` is terminal** — it does not return to `DRAFT` the way `pembelian`'s rejection does. There a rejection means "the paper was mistyped"; here it is a business decision, and looping it back would blur that into a revision and erase the only trace the request was refused. A requester who still wants the goods submits a new request.
- **`DISETUJUI` is not a wasted state.** Approval decides *how much* may leave; posting records that it did, and the two can fall on different days — hence separate `ts_disetujui` and `posted_at`.
- **`disetujui_oleh`, `ts_disetujui`, and `catatan_persetujuan` are reused for a rejection too** — the schema carries no separate `ditolak_oleh`.
- **`id_pemohon` is not `created_by`.** A clerk may type the request for someone with no account. `pemakaian_penyetuju_check` compares `disetujui_oleh` against `id_pemohon`; `Setujui`/`Tolak` catch the same rule in Go first so the message names the reason, with the CHECK as backstop. `id_pemohon` is validated only by the foreign key — anyone in `users` may be a requester.
- Posting mirrors the outgoing half of `mutasi`: `hpp_satuan_dasar`/`hpp_total` are copied from `Insert`'s `RETURNING`, never calculated in Go. `total_hpp` on the header is the sum, written once after the posting loop.
- **The second module after `mutasi` whose stock can genuinely run short**, and here it is an everyday occurrence, not a defensive branch. `periksaSaldo` is `mutasi`'s adapted to one room — not a guard, just a friendlier message, checked after the balance locks are held.
- **`KunciSaldo` is taken even though every line shares one room.** Two documents naming the same products in a different line order are a textbook ABBA regardless of room count, because the trigger locks per *insert*, not per document (`TestDuaPemakaianBersamaanProdukSamaTidakDeadlock`).
- **Cancellation writes `jenis_transaksi = 'PEMBATALAN_TRANSAKSI'`, not `'PEMBATALAN_PEMAKAIAN'`** — following the other `kartu_stok` writers. `id_kartu_stok_asal` already says what a reversal undoes; two vocabularies for one meaning is what `000021` spent itself removing. `'PEMBATALAN_PEMAKAIAN'` stays unused — `DROP VALUE` does not exist.
- Cancellation needs no `periksaSaldo`: it only ever adds stock back.
- **No unique index on `(id_pemakaian, id_product)`** — following `mutasi`: the quota is the room's balance, summed per product before checking and rechecked by the trigger.
- Prefix `PM`.

### Penjualan and the receivable side (migration 000022, isu #10)

The sales nota. Sixth writer of `kartu_stok`, and the first taking goods out **to an outside party with money on the other side**. Tables and enum predate it, so **no `ALTER TYPE`**; `000022` locks the vocabularies in CHECKs, adds `penjualan_kredit_pelanggan_check`, and swaps the list index.

- **HPP is never typed — the most expensive thing to get wrong here.** `hpp_satuan_dasar`, `hpp_total`, `total_hpp` are nullable precisely because they have no answer before posting; the application copies back what the outgoing row's `RETURNING` reported. Once `total_hpp` exists, margin is free (`total - total_hpp`), which is the only reason the column exists.
- **No `DIAJUKAN` — and, unlike `mutasi`, not because the stake is small.** Goods leave, money moves, a `KREDIT` nota creates a receivable. What rules out approval is practical: a cashier cannot make a customer wait at the counter. So the two-person control moves entirely to **cancellation** — `CASHIER` creates, types, and posts in one motion; `SUPERADMIN` alone may void. A mistyped line is corrected with a `retur_penjualan` or a supervisor's cancellation, never by the cashier who posted it.
- **`status_pembayaran` for a cash nota cannot come from the ordinary rule, so the rule is extended rather than bent.** The ordinary rule would answer `BELUM` for a `TUNAI` nota plainly paid in full, because a cash sale has no allocation at all. `RecalculateStatusPembayaran` reads `jenis_pembayaran` directly: `TUNAI` + `POSTED` is `LUNAS` by construction. `KREDIT` answers from effective allocations (see "Penerimaan pembayaran"). Still a derived cache, never set from a form.
- **`penjualan_kredit_pelanggan_check` guards something previously unguarded.** `id_pelanggan` stays nullable — a cash sale needs no customer on file, and forcing one would fill `pelanggan` with "walk-in" rows — but a receivable with no customer can be billed to nobody, has no `plafon_kredit` to check, and gives a future allocation no owner. Caught in Go first in `Create` and in `Update` against the *effective* post-patch values (the pattern `MutasiUseCase.Update` uses).
- **The price billed is a snapshot; `id_harga_jual` is a proposal that must prove itself when given.** `harga_satuan_input` is never forced to equal `product_harga_jual` — bargaining happens at the counter and the nota is what is true. When `id_harga_jual` is supplied, `siapkanDetail` validates it against `FindHargaBerlakuBatch` (resolved once for the whole basket): the version named must be the one in force for that line's product and satuan on the document's date, or the reference is more misleading than none.
- **Second module after `pemakaian` whose stock runs short as an everyday event.** `periksaSaldo` runs after `kunciJalurStok` (periode → ruang → balances), so what it reads cannot move underneath it.
- **No proportional allocation of any kind** — no freight, no nota discount split across lines, no PPN share, so `pembelian_alokasi.go` has nothing to offer. `subtotal` is a straight sum of `qty_input × harga_satuan_input − diskon_baris`; `total = subtotal − diskon_nota + pembulatan`. `diskon_nota` may not exceed `subtotal`; `total` may not go negative. `pembulatan` is typed by the cashier rather than computed to a configured multiple — a deliberate choice avoiding a new config key for a decision the issue left open.
- **`plafon_kredit` is enforced at `Posting`, under the document's own row lock.** `periksaPlafon` sums the customer's running receivable (`PiutangBerjalan`) and adds the nota; `NULL` means unlimited. **No `SUPERADMIN` override exists** — posting already sits behind `CASHIER` alone, and a bypass would put a second actor at the one moment this codebase kept to one. If needed, it belongs on the posting request as an explicit recorded field, never a silent skip. **This check has no CHECK or trigger behind it** (no constraint can compare a limit against a running `SUM`), so unlike `periksaSaldo` it *is* the guard — and it does not close the two-simultaneous-notas race.
- `GET /pelanggan/{id}/piutang` is a **read that is not a module** — `FindPiutangPelanggan` in `penjualan_repository.go`, borrowed by `PelangganUseCase`.
- **Cancellation** writes `'PEMBATALAN_TRANSAKSI'` dated `time.Now()`; `hpp_*` are **not** cleared (a snapshot of what happened; `status = BATAL` already says it no longer counts). `HasPostedAlokasi` refuses cancellation while a POSTED `penerimaan_pembayaran` allocation points at the nota (an uncashed giro counts). **Still open:** a POSTED `retur_penjualan` should block the same way, but that module does not exist.
- A photographed nota may be attached: `entity.RefTablePenjualan` plus one line in `repository.RefTableDokumen` is the entire cost.
- Prefix `PJ`.

### Stok opname dan pembekuan ruang (migration 000023, isu #15)

The physical count. Tables from `000007`, enum values since `000002`, so **no `ALTER TYPE`**; `000023` adds the freeze and the vocabulary. Seventh writer of `kartu_stok`, the first moving goods to or from **nowhere at all**, and the only one that can write **both directions in one nota without the rows ever pairing up**. It is also the only module that, while open, changes what every OTHER `kartu_stok` writer may do.

- **The primary key columns are `idstok_opname`/`idstok_opname_detail`, not `id`** — the one table spelled that way. The entity field is still `ID`; only the SQL names the column. Not renamed: foreign keys already point at them and nothing is gained by the churn.
- **Two selisih columns, not one signed column.** `stok_opname_detail_selisih_check` forbids both being positive at once, so direction is a fact rather than a sign that can get lost on a sum.
- **`id_kartu_stok_cutoff` is `NOT NULL`** — see "Goods the system has never seen" below.

#### Pembekuan ruang

While a `stok_opname` is `DRAFT` or `DIAJUKAN`, its `id_ruang` is **frozen**: no module — present or future — may post a `kartu_stok` row naming that room. The freeze lifts at `POSTED` or `BATAL`. Counting a shelf while goods move through it is guessing, and once the freeze exists the goods physically cannot leave either, so the rule matches reality rather than adding friction.

- **What is frozen is posting, not paperwork.** Drafts may still be typed and submitted in every other module; only movement is held back.
- **The radius is one `ruang`**, not a `unit_kerja` and not the company — a warehouse can be counted while its shop keeps selling. The one exception crossing a unit boundary: a `mutasi` whose **either** room is frozen is refused, even across units and sessions — a branch restocking from a warehouse being counted cannot receive the shipment, because the warehouse cannot let it leave. This is where isu #12 fase 1's "cross-unit mutasi is allowed" and this freeze meet.
- **Enforced by the `kartu_stok` trigger itself**, not by a call each module makes — the shape `periode` already uses, and for the same reason: what is protected is what every OTHER module may do, so enforcement lives where none can forget it. `kartu_stok_hitung_saldo()` reads `stok_opname` for `NEW.id_ruang` with `status IN ('DRAFT','DIAJUKAN')`, between the `periode:` lock and the `(id_barang, id_ruang)` lock, and raises `ERRCODE = '55000'` rather than `check_violation` — that code already carries two meanings and a third would make every message say "one of three things". `IsObjectNotInPrerequisiteState` + `conflictOnRuangBeku` answer **409**: the request is not wrong, the room is simply not ready.
- **Read from `status`, never `ts_verified IS NULL`.** `ts_verified` is wrong in both directions: a `BATAL`-before-verification opname leaves it `NULL` forever (freezing the room permanently), and a `DIAJUKAN → DRAFT` rejection leaves it **filled** (unfreezing a room whose count is not settled).
- **The self-reference exception**: a row whose `ref_table = 'stok_opname'` and `ref_id_transaksi` equals the freezing opname always passes, or an opname could never post the adjustment it exists to make. Checked **by value**, not by transition order, so the order two lines of Go run in can never silently break it.
- **`stok_opname_ruang_terbuka_uidx`** (partial unique on `id_ruang WHERE status IN ('DRAFT','DIAJUKAN')`) is two things at once: it stops two opnames racing open against one room (two cutoffs, the same correction booked twice) and it gives "which opname is freezing this room" a single answer, which is what the trigger reads.
- **Advisory lock order, uniform project-wide: `periode:` → `ruang:` → `(barang, ruang)`.** The trigger takes `pg_advisory_xact_lock_shared` on `hashtextextended('ruang:' || id_ruang::TEXT, 0)`; `RuangRepository.Lock`/`.LockShared` take the exclusive/shared sides. **The key expression is duplicated between migration `000023` and `repository.ruangLockKey` — if the two ever differ, neither side waits** (same warning as `periodeLockKey`).
  - `Buka`/`Create` and `Batal` from `DRAFT`/`DIAJUKAN` take the **exclusive** side. `Ajukan`/`Tolak` take **no** ruang lock — both states are frozen, so neither changes the answer.
  - **Every module that pre-locks balances** (`mutasi`, `pemakaian`, `penjualan`, and this module's `Posting`/`Batal`-from-`POSTED`) takes the **shared** side *before* `KunciSaldo`, keeping the frozen status `periksaRuangBeku` read from moving underneath the transaction — the reasoning `PeriodeRepository.LockShared` already has. `mutasi` takes **both** rooms' locks sorted by id (`kunciRuangPasangan`) — the same ABBA risk one level up.
  - `pembelian`, `penerimaan_susulan`, `retur_pembelian` do not pre-lock balances at all, so they take no ruang lock; `periksaRuangBeku` there is a plain read, like `periksaPeriode`.
- **`periksaRuangBeku` (`shared.go`) is for the message, not the guard** — the relationship `periksaPeriode` has to a closed period. Wired into `Posting` and `Batal` of all six other writers, right after `periksaPeriode`.
- **`GET /api/v1/ruang` reports which opname is freezing each room** — one `LEFT JOIN stok_opname` surfacing `nomor_opname_beku` (null when free), not a new endpoint, so a caller whose posting was refused sees the cause and who to chase without a second call.

#### Alur status

`DRAFT → DIAJUKAN → POSTED → BATAL`, plus `DIAJUKAN → DRAFT` on rejection — following `pembelian`, not `pemakaian`. A rejection here means "recount, the figures do not add up", a paper correction; a terminal rejection that never released the freeze would leave the room dead forever.

- `tgl_tutup` is stamped at `Ajukan` (when the count finished); `posted_at` is when the books were settled — possibly different days.
- `verified_by`/`ts_verified` are written by **both** `Posting` and `Tolak` (no separate rejection column, the reuse `pemakaian` makes). `Tolak` carries no reason field — there is no column to hold one.
- **`Batal` is reachable from any non-`BATAL` status**, the one deliberate departure from guarded single-source transitions: an opname abandoned while `DRAFT`/`DIAJUKAN` wrote nothing to `kartu_stok`, so cancelling is pure status change plus releasing the freeze. `alasan_batal` is required regardless.
- Prefix `SO`.

#### Opening a count and pulling the snapshot

```
POST /api/v1/stok_opname                    -- header, ts_cutoff = now(), room freezes
POST /api/v1/stok_opname/{id}/tarik-saldo   -- fill lines from the room's balance at cutoff
```

- **`ts_cutoff` is stamped by the server from `now()` at `Create`, never accepted from the body.** A client-chosen cutoff is a client-chosen selisih.
- **`TarikSaldo` reads "the balance right now", and that is correct only because of the freeze.** `KartuStokRepository.SaldoRuang` (the mirror of `SaldoPerRuang`) has no timestamp filter and needs none: `TarikSaldo` only runs on a `DRAFT`, the room has been frozen since `Create`, so "right now" and "at `ts_cutoff`" are the same figure **by construction** rather than by a query that has to prove it.
- **Refuses to run twice** (`HasDetail`) — a second pull is the cleanest way to two snapshots inside one document.
- `PUT .../detail` replaces the whole line set and is also how a missed line is added by hand — but the `id_kartu_stok_cutoff` requirement applies to every line either way.
- **`PATCH .../detail/{id_detail}` is the one deliberate exception in this API to "lines are replaced wholesale".** An opname's lines are filled in by someone walking the room product by product; resending the whole list per line would lose the count every time the network drops, and every extra minute is a minute the room stays shut. It fills `stok_so` and/or `keterangan`; the selisih columns are recomputed by the same `UPDATE` from `stok_so` against the frozen `stok_awal`, **never accepted from the request**.

#### `stok_so = NULL` is not zero

A line whose `stok_so` is still `NULL` is skipped **entirely** at posting: no selisih, no row, no change. Reading `NULL` as zero would erase that product's whole recorded stock because its shelf had not been reached yet — **the most expensive mistake this module could make**, worse than the freeze failing, because it happens silently on every partial count. `Ajukan` refuses a document with **no** line counted (an empty count is not a document) but never blocks a partial one; the response reports `jumlah_belum_dihitung` so a verifier decides with open eyes.

#### Posting: setting a balance vs posting a selisih

- **What is posted is `selisih` against the frozen `stok_awal`, never a value the balance is forced to become.** `stok_selisih_lebih = max(stok_so − stok_awal, 0)`, `stok_selisih_kurang = max(stok_awal − stok_so, 0)`, computed once by `UpdateDetailHitung`. Both columns are **always recomputed, never accepted from a form.**
- **`Posting` re-verifies the room's current balance against the frozen `stok_awal`, under the balance lock, and refuses 409 if they differ** (`periksaSaldoBergeser`). Under an intact freeze this can never fire — which is exactly why it exists: the only thing that could trip it is a bug in the freeze, and the honest response is to refuse, not to post a selisih computed against a balance that has moved.
- **Surplus is valued at the room's own moving average**, read via `SaldoTerakhir` *after* the balance lock — goods that turn up were always on the shelf, so pricing them at the average leaves it unmoved. It is the one incoming row in this codebase with **no invoice and no counterparty to copy from**.
- **Deficit is an ordinary outgoing row** and can never be refused for insufficient stock while the freeze is intact (`stok_selisih_kurang ≤ stok_awal`, still the live balance). `invalidOnCheck` stays wired as the last-resort net.
- **A line whose selisih is zero writes nothing** — `id_kartu_stok_penyesuaian` stays `NULL` forever for it.
- **If every line is zero or uncounted, the document still posts with no adjustment rows.** The one place this module and `pemakaian` disagree on principle: there an all-zero posting is pointless, here "no selisih found" is the best possible outcome of a count and must be recordable.
- **The adjustment rows are dated `ts_cutoff`, not `time.Now()`** — the selisih is a fact about the shelf at that instant, and shrinkage reporting asks about the month counted. Consequently **posting is refused if the periode containing `ts_cutoff` is `TUTUP`** — the opposite pressure from every other module's cancellation: a month closes *after* its counts post. The balance chain is untouched by the backdating, since the trigger orders by `id`, never by date.
- **Cancellation from `POSTED` is dated `time.Now()`**, like every other reversal, and can be refused outright if a surplus's goods have since left. `NilaiMasuk` on a deficit-reversal is copied from the original `NilaiKeluar`; on a surplus-reversal it is an explicit `"0"` and the trigger prices the outgoing leg. Quantity always balances; value does not always.

#### Goods the system has never seen

A product on the shelf with **no** `kartu_stok` row for `(product, this room)` cannot be counted here at all — `id_kartu_stok_cutoff NOT NULL` leaves nothing to point at, and its moving average is unknowable from a count. The alternative (nullable cutoff + a typed cost) was deliberately **not** taken: it would let inventory value be invented through a document nobody double-checks. The remedy is a `pembelian` (a missed nota) or a `mutasi`, entered **after** the opname closes — while it is open the frozen room refuses both anyway.

### Product, units, and versioned prices (migration 000011)

Three tables, one slice: `product`, `product_satuan`, `product_harga_jual`.

- **The base unit is inserted by the usecase, not the caller** — `faktor = 1`, from `id_satuan_dasar`, in the same transaction. Nothing in the schema enforces it, so a product slipping through without one would break every conversion built on it. A caller listing the base unit again collapses into that row; listing it with any other factor is a 400.
- **`product_satuan.faktor` is `BIGINT`.** Conversions must be whole; a unit holding 2.5 base units cannot be represented, and rounding it would corrupt stock arithmetic.
- **`berlaku_sampai` is exclusive and `NULL` means open-ended**, because `product_harga_jual_no_overlap` ranges over `daterange(berlaku_dari, berlaku_sampai, '[)')`. Closing a version means setting it to the **next version's start date**, not the day before — that leaves neither gap nor overlap.
- `CloseOpenHargaJual` guards with `berlaku_dari < $3`; without it a version starting on or after the new date would be closed to a date at or before its own start. A pre-existing future price is a real case.
- **Overlap is caught only by the GiST exclusion constraint** (`23P01` → 409). The check spans rows, so no pre-check in Go can replace it.
- **`is_default_input` is capped at one per product** by a partial unique index, so setting a new default *moves* the flag — `ClearDefaultSatuan` runs first in the same transaction. Two flagged units in one request are rejected in Go so the message names the field.
- A price may only be set for a unit already in `product_satuan`. **No foreign key ties `product_harga_jual.id_satuan` to `product_satuan`**, so the usecase must check.
- `kode_barang` and `id_satuan_dasar` are **absent from the update DTO** and must stay so: `kode_barang` identifies the item across every document; `id_satuan_dasar` would invalidate every `faktor` and every quantity already posted.
- `InsertSatuan` uses `ON CONFLICT DO UPDATE`, not `DO NOTHING`: a success response must never mean the stored factor disagrees with the request.
- `harga` is scanned as `harga::TEXT` — `NUMERIC(20,2)` into a `float64` rounds money on the way out.
- Detail is three queries; list is two and carries **no** children, keys omitted rather than empty.

**Harga jual siap pakai (isu #8, no migration).** Three pieces in the same slice:

- **`GET /product/{id}/harga-jual` resolves the version in force with a `WHERE`, never a `DISTINCT ON`.** The exclusion constraint guarantees at most one candidate, so there is no "latest wins" tie to break. Written as a plain range check on purpose: a `DISTINCT ON ... ORDER BY berlaku_dari DESC` would keep working (wrongly) if that constraint were ever relaxed.
- **`FindHargaBerlakuBatch` mirrors `FindFaktorBatch`** — one query for a whole basket, `unnest` of two arrays, a missing pair meaning no price in force rather than an error. `PenjualanUseCase.siapkanDetail` is the caller.
- **The WIB truncation decision.** `berlaku_*` are `DATE`; a timestamp cut down to "which calendar date" is timezone-dependent (00:30 WIB on the 15th is 17:30 UTC on the *14th*). `tanggalHargaJual` decides it once: **WIB as a fixed UTC+7 offset** (`time.FixedZone`, not `time.LoadLocation`) — Indonesia's western zone has no DST, so the offset never changes and nothing depends on tzdata being present in a container. It applies only to the default `tanggal` on `GET /product/{id}/harga-jual`; `penjualan.tanggal` never needs it, since the client sends an explicit `YYYY-MM-DD`. Pinned by `TestTanggalHargaJualMidnightBoundaryWIB` (internal test, no database).
- **`PATCH`/`DELETE /product/{id}/harga-jual/{id_harga}` are both refused 409 once a `penjualan_detail` row references the version.** `harga_satuan_input` is already a snapshot, so touching the master never changes what a document billed — but it would leave `id_harga_jual` pointing at a row no longer describing the version that document came from, and that reference is the only trace of which version that was. `HargaJualDipakaiDokumen` is the shared guard, checked inside the same transaction as the write.
- **`PATCH` accepts only `harga`.** `berlaku_dari` shifts which range a version covers and can collide with a neighbour, so that correction is delete-and-retype — which is also what should show up in the trail. `id_satuan` is immutable for the reason `kode_barang` is.
- **`DELETE` here is hard** — the third exception to "master data has no `DELETE`", after `user_role` and `dokumen`. The row it may remove is one no document references, so there is no audit trail to lose.
- **`DELETE` always reopens the previous version, in the same transaction.** `ReopenPreviousHargaJual` is the exact inverse of `CloseOpenHargaJual`: it hands the deleted version's `berlaku_sampai` to whichever version ends at the deleted version's `berlaku_dari`. Skipping it leaves a **gap** — a date range with no price, which the resolver then answers "no price" for. A version with nothing before it has nothing to reopen; zero rows affected is the honest answer, not an error.
- **`GET /product/harga-jual` (no `{id}`) is the cross-product list**, a read that is not its own module. Registered **ahead of `/product/:id`** in `route.go` so the literal segment is not swallowed by the parameter (`TestStaticSegmentBeatsParamAtSamePosition`).
- **That list is `LEFT JOIN`, and the date filter lives in the join's `ON` clause, not in `WHERE`.** A product with no price in force on the requested date is exactly the row the list exists to surface; putting the range check in `WHERE` silently turns the `LEFT JOIN` back into an inner join and drops precisely those products. `daftarHargaFrom` is shared between `COUNT` and rows.
- **`ORDER BY` ends in `(p.id, h.id_satuan)`.** `p.id` alone is unique per product but not per row, since one product can carry several priced satuan.

### Users and roles (migration 000010)

One user holds many grants; `user_role` is the only record of that. What a user *may* do is the union of every grant they hold; what a **session** authorizes as is exactly one of them at a time. **Don't conflate the two.**

- **`users.role_active` is gone.** Migration `000002` declared `UNIQUE (role_active)` — unique across the whole table rather than per user, so the system could hold exactly one cashier. Its FK also pointed at `user_role (id)` without `user_id`, so user A's active role could point at user B's grant. `000010` drops it rather than repairing it. **Don't reintroduce it**; a "default module on login" preference would be a UI preference column, not a permission gate.
- **Roles are seeded, then editable.** `PATCH /api/v1/role/{id}` can rename them, and **renaming a role that authorization code checks by name breaks that code** — nothing in the database can catch it. Retire with `is_aktif = false` and add a new role instead.
- **A grant is `(role, unit_kerja)`.** `grants` on `POST`/`PATCH /api/v1/user` replaces the whole set: absent leaves grants alone, `[]` revokes everything, a list ends with exactly those. An explicit `null` is rejected, because `[]` already says "no grants".
- **The same role may be granted more than once per user, one row per unit.** `usecase.toGrants` deduplicates by the `(id_role, id_unit_kerja)` **pair**, not by role id. `entity.User.Roles` is `[]entity.RoleGrant`, so the same role name can legitimately appear twice.
- **`ReplaceRoles` is a diff, not delete-then-insert** — surviving grants are left alone so `created_at` keeps saying when the grant started. See "Unit kerja (isu #12)" for the `NULL`-safe comparison and the two-`ON CONFLICT`-statement insert.
- `user_role` is the one table where `DELETE` is correct: a join table no transaction table references, so revoking breaks no foreign key and erases no history (`created_by` points at `users`, not at `user_role`).
- **A roles-only patch still has to move `updated_at`** — that is `UserRepository.Touch`: writes no other column, fires the trigger, and yields `sql.ErrNoRows` so an unknown id still answers 404.
- **Role ids and unit ids are both validated before the write**, not left to the foreign key (which cannot tell retired from live and names a constraint rather than a field). `CountActiveByIDs` compares a count against the number of **deduplicated** ids — pass duplicates and a valid request is wrongly rejected. `IsForeignKeyViolation` is the race backstop.
- **Passwords are bcrypt hashes, hashed in the usecase.** `model.UserResponse` has no password field at all, which makes a leak structurally impossible rather than a matter of remembering. bcrypt refuses input over 72 **bytes** while the DTO's `max=72` counts **runes**, so `hashPassword` maps `bcrypt.ErrPasswordTooLong` to `model.Invalid`.
- Attaching grants to a page is **one extra query, not one per user** (`FindRolesByUserIDs` with `= ANY($1)`). `pgx/stdlib` implements `CheckNamedValue`, so a Go `[]int64` (and `[]*int64` for a nullable `BIGINT[]`) passes through `database/sql` untouched.
- The `role_id` list filter is an **`EXISTS`, never a join** — a join returns one row per matching role and silently multiplies the page, breaking both `LIMIT` and `total_item`.
- A user's grant list **includes grants whose role or unit was retired** — the grant is still real and still needs revoking. `RoleRef.is_aktif` / `is_aktif_unit_kerja` tell them apart. Filtering down to what a session may act as happens only in `attachRolesForLogin`/`grantUsableBy`.
- `username`, `email`, and `role.nama` are unique **case-insensitively** via `lower(...)` indexes. `email` is nullable, so any number of users may have none.

### Unit kerja, active context, and scoping (isu #12, migrations 000019–000020)

Answering not just *who* but *acting as what, and where*. All five load-bearing phases are built, plus the read-scoping piece of the optional phase 6. **Deferred, not overlooked: `users.id_ruang_default` and a role-as-snapshot column on documents.**

**This is not `users.role_active` revived.** A grant is a row a caller *holds*, not a pointer to "the" active one; a user can hold the same role at two units as two rows, which `role_active` structurally could not express. The active context lives in the JWT, never in a column on `users`.

#### Fase 1 — three decisions made up front

- **`document_counter` should get a per-unit series** — key `(prefix, id_unit_kerja, tahun, bulan)`. **Not implemented yet**; `Next` still keys on the old triple. **Change the key before any two real outlets share one series** — once a number is on paper in a supplier's hands, the key cannot change under it.
- **`periode` stays global**, one close/open per `(tahun, bulan)` for the whole company. An outlet closing August while another posts into it produces a consolidated report nobody can explain. If per-unit closing is ever needed, `periode` gains `id_unit_kerja` and the advisory-lock key must grow the unit into **both** copies of the expression (migration `000017` and `periodeLockKey`) or one side stops waiting for the other.
- **Cross-unit `mutasi` is allowed.** Moving stock between outlets is what `mutasi` is for; restricting it would leave no document for restocking a branch from the central warehouse. Posting stays partitioned by `(id_barang, id_ruang)`, never by unit.

#### Fase 2 — the `unit_kerja` master and `ruang.id_unit_kerja` (migration 000019)

- **A plain master slice — follow `supplier`**: nullable case-insensitive unique `kode`, `Optional[T]` PATCH, retirement via `is_aktif`, no `DELETE`. Carries only `kode`, `nama`, `is_aktif`, audit columns. `nama` is **not** unique. Uses `supplier`'s `ExistsByKode` (optional, checked only when supplied), not `satuan`'s `ExistsByNama`.
- **`ruang.id_unit_kerja` is `NOT NULL`, and the backfill lives in the migration, not the seeder.** The migration creates one default unit (`kode = 'PUSAT'`) and points every existing `ruang` at it **before** adding the `NOT NULL`, in the same transaction — that is what makes it safe on a database that already has rows. A seeder backfill would only ever be safe on a fresh database, since nothing guarantees a seeder runs before a later migration's `NOT NULL`. `001_ruang.sql` points its rows at `'PUSAT'` **by lookup, not by a hardcoded id**.
- `RuangUseCase.Create` validates `id_unit_kerja` names an *active* unit — the foreign key cannot tell retired from live. `ruang` still has no `PATCH`, so a room's unit cannot change through the API.

#### Fase 3 — `user_role.id_unit_kerja` (migration 000020)

A grant is the pair `(role, unit_kerja)`. **`NULL` means "every unit"** — the shape the seeded `SUPERADMIN` grant takes and every pre-migration grant keeps.

- **Two unique indexes, not one, because a unique index does not constrain `NULL`.** `user_role_grant_uidx` on `(user_id, role_id, id_unit_kerja)` covers scoped grants, but PostgreSQL treats `NULL <> NULL`, so it alone would allow ten identical *global* grants. `user_role_grant_global_uidx` — partial on `(user_id, role_id) WHERE id_unit_kerja IS NULL` — closes that. **Both are required.**
- **`ReplaceRoles`'s diff had to become `NULL`-safe, and the old code could not be patched in place.** The delete used to be `role_id <> ALL($2)`; that comparison is `NULL` (not `TRUE`) whenever either side is `NULL`, so a plain port would silently keep every global grant no matter what the replacement set said. The fix is `NOT EXISTS (... WHERE t.role_id = ur.role_id AND t.id_unit_kerja IS NOT DISTINCT FROM ur.id_unit_kerja)` — the one comparison PostgreSQL defines to treat two `NULL`s as equal. `TestUserRevokingGlobalGrantActuallyDeletesIt` pins it.
- **The insert is two statements, for the same reason.** One `INSERT ... ON CONFLICT` names exactly one arbiter index, and scoped/global grants are protected by two different ones. `ReplaceRoles` filters its input in Go and runs each half against its own arbiter — PostgreSQL has no syntax for satisfying both at once.
- **The DTO changed shape, not just name**: `role_ids` (`Optional[[]int64]`) → `grants` (`Optional[[]model.GrantRequest]`), where `GrantRequest` is `{id_role, id_unit_kerja}` with a nullable optional pointer. `Optional[[]model.GrantRequest]` **must stay registered in `config.NewValidator`** or its `dive` tag silently stops validating each entry.
- **`id_unit_kerja` is validated active independently of `id_role`, with its own message** — a bad role and a bad unit are different problems. Both are pre-checks; `invalidOnForeignKey` is the race backstop.
- **`FindRolesByUserIDs` still costs one query per page** (`LEFT JOIN unit_kerja` alongside the existing `JOIN role`), and still returns grants whose unit was retired.
- `004_superadmin.sql`'s `ON CONFLICT` now names the **partial** index (`(user_id, role_id) WHERE id_unit_kerja IS NULL`) — the seeded grant is, and must stay, global.

#### Fase 4 — active session context, `POST /auth/switch-context` (no migration)

A token authorizes as **one** active grant, not the union of everything held. Lives entirely in the JWT claims and the two usecases that mint them.

- **`model.Grant` and `model.ActiveContext` are one pair of types for three jobs** — the JWT claim shape, `Session`'s fields, and what `LoginResponse`/`SessionResponse` return. Only `entity.RoleGrant` needs `toGrantList`/`toActiveContext` to cross over.
- **`Session.HasRole` compares the active grant alone, never the full list** — that one line is the entire enforcement mechanism. `RequireRole` and every guard in `route.go` are **byte-for-byte unchanged**; a session with `Aktif == nil` fails every check by construction.
- **Login auto-selects when there is no ambiguity and refuses to guess otherwise.** Exactly one usable grant becomes active automatically; two or more issue a token with `Aktif: nil`, which authorizes nothing until `switch-context`. There is no default among several that would not risk someone acting under an authority they did not realize they picked. `auth/me` and `switch-context` remain reachable — neither carries a `RequireRole` guard.
- **`grantUsableBy` is the single predicate `Login`'s filtering and `SwitchContext`'s validation share**, so the two cannot drift on what "usable" means (role active, unit active-or-absent, ownership).
- **`SwitchContext` re-reads the grant from the database — the one place in the whole design a token's claims are not trusted.** The caller is naming a grant by id, and a stale token could name one since revoked or retired. A deliberate, narrow exception to "no per-request lookup", scoped to exactly one endpoint.
- **Every rejection collapses to one 403** (`"grant does not exist or is not usable"`) — distinguishing them would let a caller probe which grant ids exist for other users.
- `SwitchContext` takes a `userID int64`, not a `*model.Session` — the controller extracts it via `middleware.SessionFrom`.
- **Switching cannot revoke the token being switched away from, and is not trying to.** The old token stays valid until expiry. Same limitation as everywhere; **do not "fix" it with a blacklist.**
- **This is not a security boundary against the token's own holder** — it is a least-privilege and clarity control. Holding two grants means both are yours; switching only changes which is active.

#### Fase 5 — `id_ruang` validated against the active unit (no migration)

`unit_kerja → ruang` is one-to-many, so the active unit never implies *which* room a document means. The client still picks; the id it sends is now checked.

- **`periksaRuangUnitAktif` (`shared.go`) is the shared function**, the role `periksaPeriode` plays for closed months — except it guards nothing on the database side, because `ruang.id_unit_kerja` cannot change (no `PATCH`), so there is no race. A `nil` `aktifIDUnitKerja` skips the check entirely — the reading `id_unit_kerja IS NULL` carries everywhere. An **unknown `id_ruang` is deliberately let through**: that failure belongs to the foreign key.
- **Validation, not a default.** `AktifIDUnitKerja *int64` rides on the request DTOs, filled by the controller from `session.Aktif.IDUnitKerja` via `aktifIDUnitKerja(ctx)` — never from the body, and never used to substitute a room the client didn't ask for. A server filling in a default while still accepting whatever `id_ruang` the body sent would make the scoping decorative.
- **Only `pembelian`, `mutasi`, and `stok_opname` have `id_ruang` in their own request body, so only they check it.** `penerimaan_susulan`/`retur_pembelian` copy it from the parent, whose `Create` already validated it. `pembelian`'s `PATCH` has no `id_ruang` field; `mutasi` needs it on `Create` **and** `Update` (both rooms may change while `DRAFT`); `stok_opname` only on `Create`.
- **`mutasi` checks `id_ruang_asal` only, never `id_ruang_tujuan`.** The active unit is exactly one unit, so requiring both would make every permitted mutasi same-unit in practice, quietly reversing fase 1's decision. Checking the source alone matches the authority actually asserted: goods are leaving a room the caller is responsible for; where they land is not a claim about their own authority.
- `mutasi`'s `Update` checks only when `id_ruang_asal` is present in the patch, and only the new value — mirroring the `asal <> tujuan` check just above it.
- **`RuangRepository.IDUnitKerjaByID`** is a one-column read, not `FindByID`'s join — this check needs only the unit id, on a path every write now takes.
- `PembelianUseCase`, `MutasiUseCase`, and `StokOpnameUseCase` each borrow `RuangRepository` for it — the "borrow a repository for a narrow read" shape.
- **`users.id_ruang_default` was deliberately not built.** It saves the client one field; it is not an authorization boundary. Add it only if the convenience is asked for, and **never let it double as validation.**
- **`pemakaian` and `penjualan` deliberately opt out** of both fase 5 and fase 6 — neither issue asked for the scope, and neither module adds it on its own initiative.

#### Fase 6 — read-path scoping (no migration)

Built after being explicitly asked for. Scoped: `ruang`, `pembelian`, `penerimaan_susulan`, `retur_pembelian`, `mutasi`, `stok_opname`, and `product/{id}/stok`.

- **What "scoped" means depends on the shape of the read.** A `Get` answers **404**, not 403, for a resource outside the active unit — the same answer an id that never existed gets, on purpose: a 403 would itself confirm the resource is real. A `List` (and `product/{id}/stok`, which is list-shaped) simply **omits** rows, silently, the way a page with no matches always looks.
- **`diLuarUnitAktif` (`shared.go`) is deliberately a separate function from `periksaRuangUnitAktif`**, not a shared one: the write-side check returns `model.Forbidden`, and reusing it would make a scoped `Get` leak exactly the fact it exists to hide. It returns a bare `bool`; every call site maps `true` to `model.NotFound` itself, so the 403-shaped mistake must be written out loud rather than inherited. A `nil` active unit excludes nothing.
- **Every scoped module gained one column alongside what it already read, never a second query** — an unexported `IDUnitKerjaRuang int64` on the entity, joined into the query that already joins `ruang` for its name. One column added to each `xReadColumns`, one scan target added to each `scanXRead`. `mutasi`'s is named `IDUnitKerjaRuangAsal` and comes from `asal.id_unit_kerja`.
- **The check lives in the `detail()` helper, threaded as a parameter rather than read from ambient state.** `Get` (and `pembelian`'s `Sisa`) passes the caller's real active unit; **every write-path call — `Create`'s re-read, `Ajukan`, `Posting`, `Tolak`, `Batal`, `ReplaceDetail`, `BagiRataKoli` — passes `nil`.** A caller who just posted a document is by construction allowed to see the response their own action produced; scoping that re-read would make a legitimate action look like a 404.
- **A filter clause reaching a joined table forces the `COUNT` query to join it too**, and four modules had to change for exactly that: each `Search` had been running `COUNT` against a bare `FROM table` that never reached `ruang`. Changing it to use the same `xFrom` constant as the row query is what "write the filter once, both queries use it" actually requires. `mutasi` needed the same fix even though `mutasiFrom` already joined both rooms for their names.
- **`mutasi` inherits fase 5's source-only asymmetry on the read side too.** A transfer whose destination is in another unit stays fully visible to whoever owns the source room; a caller who owns only the destination cannot see it at all, even though the goods are headed there. **Visibility follows the room a caller asserts authority over, not every room the document touches.** `TestMutasiGetVisibleWhenOnlyDestinationRuangOutsideActiveUnit` pins the case a reader arriving from the other modules would expect to be scoped and isn't.
- **`GET /product/{id}/stok` is scoped in `KartuStokRepository.SaldoPerRuang`, and the filter sits on the *outer* query**, after `DISTINCT ON (ks.id_ruang)` has picked one row per room. Filtering earlier could only change which row wins each group, and since the key *is* the room it cannot change any surviving row — keeping the filter outside makes that true by construction rather than by argument.
- **Query-string spoofing is closed the way `ActorID` already is.** Every `AktifIDUnitKerja` field carries `json:"-"` (body-bound) or `query:"-"` (query-bound; Fiber v3's binder is `gorilla/schema` under an alias tag — confirmed against the vendored source, not assumed), and the controller **overwrites the field unconditionally after binding** regardless of whether the tag alone would suffice.
- **The freeze in `stok_opname` is never scoped by unit** — the trigger refuses by room, not by who is posting.
- Tests live in `fase6_read_scope_test.go`, one file spanning every scoped module, so the one real asymmetry reads as a deliberate exception rather than a forgotten module.

### Periode and book closing (migration 000017)

The act that makes a month refuse further stock movements. The tables and the trigger's respect for them predate this by fourteen migrations; what was missing was any Go at all.

- **Master-data shaped but cross-cutting — follow `supplier`, not `pembelian`.** No number, no lines, no posting. What makes it unlike a master slice is that the row it writes is read by the `kartu_stok` trigger on every insert, so **every stock-writing module inherits the refusal without a line of code**. That is also why the write guard is `SUPERADMIN` rather than a data owner: closing a month is not this module's data changing, it is every other module losing the ability to post into it.
- **A month with no row is open**, which shapes everything: closing **creates** the row (hence the upsert), `Get` answers a synthetic `BUKA` rather than 404, and `Search` cannot list months nobody has touched — the table records closings, not a calendar.
- **Routes are keyed `(tahun, bulan)`, not `/{id}`** — the only module departing from that. The pair is the real identity, and an id-keyed route could not address the ordinary case at all, since an unclosed month has no id. The response carries **no `id` field**, so a stored and a synthetic month have the same shape.
- **The reversing-row date is the decision this issue was really about.** Posting is dated on the document; cancellation is dated `time.Now()`. So **voiding a document whose period has since closed still works** — the reversal lands in the current period and the closed month's figures do not move. The alternative leaves a mistyped document from a closed month with no way out. The cost is stated plainly in `PembelianUseCase.Batal`: the document reads `BATAL` while the closed month's ledger still carries its movement, so **anything reporting per period must read `kartu_stok`, never the document status.** What *can* block a cancellation is the **current** period being closed.
- **Closing and posting are serialised by an advisory lock, not a row lock.** The trigger takes `pg_advisory_xact_lock_shared` on `hash('periode:' || tahun || '-' || bulan)`; `PeriodeRepository.Lock` takes the exclusive side. `SELECT ... FOR SHARE` was rejected because an unclosed month **has no row** — precisely the case that matters. **The key expression is duplicated between migration `000017` and `periodeLockKey`; the two must produce the same string or neither side waits.** `TestTutupMenungguPostingYangSedangBerjalan` fails against the pre-`000017` trigger, so it is a real test.
- The periode lock shares the `hashtextextended(..., 0)` key space with the `(barang, ruang)` and `ruang:` locks, separated only by prefix. A 64-bit collision costs an unrelated writer a short wait, never a wrong answer. **The periode lock is taken first, uniformly**, so there is no path to a deadlock.
- **Reopening is allowed, `SUPERADMIN` only**, and `000017` adds `dibuka_oleh`/`ts_buka` for it — without them, closing after a reopening overwrites `ditutup_oleh`/`ts_tutup` and nothing records the month was ever reopened. `Tutup` therefore leaves the reopening columns alone. A pair of columns rather than an audit table: full history is a different question, answerable when actually asked.
- **`Buka` is an UPDATE, not an upsert, and the asymmetry is the point.** Reopening a month with no row would insert a row saying `BUKA`, which is what a missing row already means. It repeats `status = 'TUTUP'` in the `WHERE`, so `sql.ErrNoRows` covers both "never closed" and "someone reopened it first" — one message, a 409. Closing an already-closed month is a 409 too: neither changes anything, and a 200 would let a caller believe otherwise.
- **Closing is not required to be sequential.** August may be closed while July is open — requiring an order would force closing every unused month first, and enforcement is per month inside the trigger, not a running total.
- **`periksaPeriode` (`shared.go`) is for the message, not the guard** — a trigger's `RAISE` carries no constraint name, so `invalidOnCheck` cannot separate a closed period from insufficient stock. It runs in `Posting` (on the document's date) and `Batal` (on **today's**, since that is what the reversal is dated), answering 400 to match the trigger's mapping. A closing that commits between check and insert is still caught, just with the vaguer message.
- No `created_at`/`updated_at` and no trigger — `ts_tutup`/`ts_buka` already answer the useful question.

### Dokumen and file attachments (migration 000016)

Uploaded files attached to whichever document they belong to. Two things make it unlike every other slice: it holds a store that is not the database, and its reference is polymorphic.

- **Upload first, attach later, forced by the physical world.** The photo is taken while the box is being opened, before the `pembelian` exists. A row is born with `ref_table`/`ref_id` NULL and `POST /dokumen/{id}/tempel` claims it afterwards. **The nullable `ref_id` is the feature** — it makes an orphan possible and, through a partial index `WHERE ref_id IS NULL`, cheap to find.
- **Attaching is one endpoint, not a `dokumen_ids` field on every document.** A module that starts accepting attachments adds one line to `repository.RefTableDokumen` — no migration, no DTO change. That map is also the only thing standing between `ref_table` and an arbitrary string (there is no foreign key behind a polymorphic reference), and why `StatusRef` may interpolate a table name into SQL at all: what reaches the query is a key of the map, never the caller's string.
- **The write order cannot be swapped.** Upload writes the file, then the row, deleting the file if the row fails; deletion goes file first, then `deleted_at`. Same rule both ways — whichever half survives a crash has to be the recoverable one. A row pointing at a missing file cannot be repaired by anyone, because nothing knows what the file should have contained.
- **MIME comes from the bytes, extension from the MIME, storage name from a UUID.** The client's filename is display text and reaches the filesystem never. `LocalDokumenStorage.path` **rejects** rather than sanitises: `filepath.Base` would quietly turn a traversal into a write to the wrong place, and a bug that makes no sound is worse than an error.
- **The size limit is enforced on the stream** (`io.LimitReader`, reading one byte past the limit so "exactly at" is distinguishable from "truncated"). `Config.BodyLimit` is derived from `dokumen.max_size_mb` in `config.NewFiber` — Fiber's 4 MB default would otherwise silently cap a configured 10 MB.
- **Downloads stay behind the token and always as an attachment** — `Content-Disposition: attachment` plus `X-Content-Type-Options: nosniff`, so a stored HTML or SVG cannot execute in the application's origin. `dispositionLampiran` reduces the quoted form to safe ASCII and carries the real name in RFC 5987 `filename*`: `nama_asli` is arbitrary client bytes, and a CR in a header value is header injection.
- **The cleanup job works from rows, never a directory scan** — a scan would sweep up files whose row is written but not yet committed. Safe against a second worker in two layers: a session-level `pg_advisory_lock` (on a `*sql.Conn`, since releasing from another pooled connection does nothing) and one transaction with a row lock per file, so an attach racing a sweep blocks instead of losing its bytes.
- **Soft delete** — the row survives with `deleted_at`, only the file goes. That trace is what makes the sweep re-runnable.
- Removal is allowed **only while orphaned or while the parent is `DRAFT`**. Attaching to a `BATAL` parent is refused for the mirror reason: it could never be removed again.
- **No role guard beyond being authenticated**, and that is not the reads-are-open rule stretched over writes: attachments belong to no module, so no data owner can be named. **State protects them** — inert until claimed, refused past `BATAL` or ten files, unremovable past `DRAFT`.
- Config under `dokumen.*`, read by **both** entrypoints. `dokumen.storage_path` must resolve to the same directory in `web` and `worker` (one volume mounted on both in compose) — point them apart and the sweep finds rows, finds no files, and marks them deleted anyway.
- `checksum_sha256` reports `duplikat_dari_id` and never refuses: one scan legitimately belongs to two documents.

### Reads that are not modules

#### Stok per ruang

`GET /api/v1/product/{id}/stok` — and the **first read of `kartu_stok`**. Three repository methods, built as their own phase because everything after wants them: `SaldoTerakhir` (one pair), `SaldoBatch` (many pairs, one query, `unnest`), `SaldoPerRuang` (one product across rooms). `SaldoBatch` backs every module's negative-stock pre-check.

- **A pair with no rows is a balance of zero, not a missing record** — the reading the trigger takes when it COALESCEs, and the shape `periode` uses for an unclosed month. `SaldoTerakhir` returns the zero value; `SaldoBatch` omits the key.
- **It is a read and never a guard.** The balance is decided inside the trigger under an advisory lock precisely so no reader can get in front of it.
- **No pagination** — one row per room the product has moved through; `ruang` is small and every caller wants all of them. Rooms the product has never been in do not appear; a room that emptied out still does, with zero. Unknown product → 404; a product that has never moved → empty list.

#### Riwayat harga beli

`GET /api/v1/product/{id}/riwayat-beli` — the replacement for the purchase order this system deliberately does not have, and the worked example of a read that is not a module.

- **Nothing new is stored.** Every POSTED `pembelian_detail` row is already a price actually paid, which is worth more than a quotation. A table for this would create a second source of truth.
- **SQL in `pembelian_repository.go`, endpoint hangs off product.** `ProductUseCase` borrows `PembelianRepository` — the query is over another module's tables, so it stays in that module's repository.
- **Two prices, and collapsing them is the mistake to avoid.** `harga_satuan_dasar` (= `subtotal / qty_dasar`) is the invoice per base unit and what a supplier's next quote is compared against; `harga_pokok_satuan_dasar` is after nota discount, PPN share, and freight, and is what margin is judged against. Negotiating with the second holds the supplier responsible for the carrier's bill; costing with the first drops freight entirely. `harga_satuan_input` is reported but **not comparable** (per input unit).
- **The product is looked up first**, so an unknown id answers 404 and a product nobody has bought answers an empty page — different facts, and a client that cannot tell them apart shows the wrong message.
- **Only POSTED** — a DRAFT is a typed page and a BATAL was withdrawn; one condition covers both.
- `DISTINCT ON (p.id_supplier)` forces the inner `ORDER BY` to lead with `id_supplier`, hence the wrapping query that re-sorts by date; the outer `ORDER BY` ends in `id_supplier`, unique across the subquery *because* `DISTINCT ON` made it so. The inner tiebreaker `d.id DESC` matters: one document may carry the same product twice.
- The division is safe because `qty_dasar > 0` is a CHECK. Casting to `NUMERIC(20,4)` rounds halves away from zero, matching `formatNumeric`.

### Inventory data model (migrations 000002–000008)

Read these invariants before writing any inventory usecase; several are enforced by the database and will reject wrong code at runtime. `pembelian_usecase.go` is the worked example.

- **`kartu_stok` is the only source of truth for stock and inventory value.** No master table carries a stock column. **Never compute stock by summing documents.**
- **It is append-only, enforced by trigger** — `UPDATE`, `DELETE`, `TRUNCATE` all raise. Corrections are new reversing rows filling `id_kartu_stok_asal`.
- **The trigger computes the balance, not the application.** On insert it overwrites `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, `nilai_akhir`. A usecase supplies only the direction (`stok_masuk` **or** `stok_keluar`, never both), `nilai_masuk`, and the reference columns. A computed balance sent in is silently discarded.
- **Moving average:** incoming rows shift `harga_pokok_satuan`; outgoing rows never do. Stock reaching zero forces `nilai_akhir` to exactly 0 so rounding residue cannot accumulate.
- Balance is partitioned by `(id_barang, id_ruang)` and ordered by **`id`, not date**.
- The trigger raises on negative stock, on posting into a `TUTUP` periode, and on posting into a `ruang` frozen by an open `stok_opname`. A month with no `periode` row is open; a room with no open opname is free.
- Quantities in `kartu_stok` are always in the base unit; `qty_input`/`id_satuan_input` are an audit trail of what the operator typed.
- **Documents store snapshots** (`faktor_konversi`, prices, HPP); master data stores current rules. Master may change; snapshots never do. **Returns must copy cost from the originating detail row**, not from the current average, or a sale and its return do not cancel out.
- Stock moves only on posting, never on draft.
- **Receivables and payables are mirror images.** Payment↔invoice is many-to-many, which is why allocation is its own table.
- `penjualan.status_pembayaran` and `pembelian.status_pembayaran` are **caches**; never let a form set either.
- **An uncashed giro is not a payment.** The balance drops only when `status_giro` becomes `CAIR`.
- **Overpayment is normal**: `jumlah_dialokasikan` may be less than `jumlah`. **Never force allocation to balance exactly.**
- **Freight is allocated by koli** (`metode_alokasi_angkut` defaults to `'KOLI'`): `alokasi_biaya = (jumlah_koli / total_koli) × biaya_angkut`. `jumlah_koli` is decimal because one line can occupy part of a koli. When every `jumlah_koli` is zero, fall back to `QTY` rather than dividing by zero. The method is stored per document, so changing it later never rewrites old allocations.

Enforced by CHECK constraints — **don't re-validate in Go, just surface the error**: one-way movement, non-negative quantities and values, `faktor > 0`, `qty_dasar > 0`, `jumlah_koli >= 0`, `mutasi` source ≠ destination, requester ≠ approver in `pemakaian`, `bulan` 1–12, `jumlah > 0` and `jumlah_dialokasikan <= jumlah` on both payment tables, and non-overlapping `product_harga_jual` ranges (GiST exclusion needing `btree_gist`).

**Still application-side only, and still owed: `retur_penjualan`'s cumulative-quota check** — the sole remaining item, the mirror of what `retur_pembelian` already closed. Everything else on that list (base-unit `product_satuan` row, koli balance and `bagi rata`, exact `alokasi_biaya` sums, posted documents rejecting detail edits, reversing rows on cancellation, follow-up and return quotas, returns reducing the payable, allocation ceilings on both sides, cancelled documents refusing allocations, recomputed `status_pembayaran` on both sides, `plafon_kredit`, one-transaction writes) is done. **The "HPP copied from the original" half of the reversal rule was dropped as unachievable** — the trigger overwrites `harga_pokok_satuan`/`nilai_keluar` on every outgoing row.

## Conventions

### Adding a module

Follow the `supplier` slice, in this order: migration in `db/migrations_postgres/` (**most inventory tables already exist — check first**) → `entity` → `model` DTOs → `model/converter` → `repository` (methods take `DBTX`) → `usecase` (validate, own the transaction) → `delivery/http` controller → register in `route.RouteConfig` → wire in `config.Bootstrap` → update `docs/openapi.yaml` **and `README.md`'s endpoint table**.

Pick the template by shape from the table at the top of this file. Do not copy a master slice for a transaction document — you will leave out the row lock on every transition, the exact-decimal arithmetic, and the reuse of `DocumentCounterRepository`/`KartuStokRepository`.

**Every module that writes `kartu_stok` — including any built after this one — must call `usecase.periksaRuangBeku` at `Posting` and `Batal`, naming the room(s) it writes into.** The freeze itself needs no code (the trigger is unconditional); what a new module can forget is the *message*. **If the module pre-locks balances** (`KartuStokRepository.KunciSaldo` before the insert loop), it must also take `RuangRepository.LockShared` on every room it touches, **before** `KunciSaldo`.

**Master data gets no `DELETE`** — every master table is referenced by transaction tables, so deleting a used row either fails on a foreign key or destroys the audit trail. Retire with `is_aktif = false`. Three exceptions: `user_role` (a join table nothing references), `dokumen` (soft — the row stays with `deleted_at`, only the file goes), and `product_harga_jual` (hard — the row it may remove is one no document has ever referenced, so there is no trail to lose).

### PostgreSQL specifics

No ORM, so these are hand-written every time:

- Placeholders are `$1, $2, …`, not `?`. `LastInsertId()` is unsupported — use `RETURNING id` with `QueryRowContext(...).Scan(&id)`.
- Always use the `…Context` variants. Note the limit: Fiber v3's `c.Context()` defaults to `context.Background()` and is **not** cancelled on client disconnect, so a slow query is not aborted for free — attach an explicit timeout where one matters.
- Identifiers fold to lowercase unless quoted; keep names lowercase `snake_case`. Use `TIMESTAMPTZ`, never `TIMESTAMP`. `updated_at` is maintained by the `set_updated_at()` trigger, not from Go.
- **Every `ORDER BY` paired with `LIMIT`/`OFFSET` ends in a unique column** (`ORDER BY nama, id`). Ordering on a non-unique column alone lets one row appear on two pages while another is never returned — a live bug the moment data outgrows a page.
- **Every search string goes through `repository.EscapeLike`.** Unescaped, a user's `%` matches everything and a product literally named `100%` can never be found. A correctness bug, not an injection one.
- **Write the filter once** as a package-level constant used by both the `COUNT` and the row query — two copies drift and `total_item` disagrees with the rows. **If the filter reaches a joined table, the `COUNT` must share the same `FROM` constant.** Keep the filter on `$1..$N` with pagination placeholders after it.
- **Never `SELECT *`.** Declare the column list as a constant so `Scan` order cannot drift when a migration adds a column.
- Nullable columns must be scanned into a pointer or `sql.NullXxx`.
- **A unique index does not constrain `NULL`s.** Any number of rows may share `kode = NULL`; only call `ExistsByKode` when a kode was supplied. For a nullable column that must still be unique when absent, add a **partial** index (see `user_role_grant_global_uidx`).
- Uniqueness on master codes is **case-insensitive** via `lower(...)` indexes (migration `000009`) — existence checks must use `lower(...) = lower($1)`.
- **Check-then-insert never guarantees uniqueness.** `repository.UniqueViolation` maps `23505` so the loser of the race gets a 409 rather than a 500; the pre-check stays only for the friendlier message.

### PATCH semantics

A pointer cannot tell "field absent" from "explicitly null", and `COALESCE($n, col)` silently keeps the old value for both — so an operator could never clear a mistyped phone number. Every partial update uses `model.Optional[T]`, whose `UnmarshalJSON` records that the key was present:

- Nullable columns: `col = CASE WHEN $n::BOOLEAN THEN $m ELSE col END`, flag fed from `Optional.Present`.
- `NOT NULL` columns: `COALESCE` is correct, and an explicit `null` is rejected with `model.Invalid`.
- `UPDATE ... RETURNING` supplies the response; `sql.ErrNoRows` means the id does not exist. **Never `SELECT` first to check** — two queries, still racy.
- `id`, `created_at`, `created_by` never appear in an update DTO. The controller binds the body, then overwrites `ID` from the path.
- **Tags on an `Optional` field must lead with `omitempty`, and each instantiation must be registered in `config.NewValidator`** or its validation tags are silently ignored. Registered today: `Optional[string]`, `Optional[bool]`, `Optional[[]int64]`, `Optional[int64]`, `Optional[[]model.GrantRequest]`.
- A collection field works the same way, meaning replace rather than set: absent leaves it alone, `[]` empties it, a list replaces it wholesale. `dive` does reach elements through the custom type func (`TestValidatorDivesIntoOptionalSlice`).

### API contract

`docs/openapi.yaml` is the contract. Update it in the same change as any route, request, or response shape change.

It is also a **build input**: `docs/docs.go` pulls it in with `go:embed`. Two consequences — dropping `docs/` from the Docker build context **fails compilation** rather than merely losing the docs page (`.dockerignore` deliberately does not exclude it), and a malformed `openapi.yaml` is still served happily, since `go:embed` copies bytes and does not parse YAML.

`gofiber/contrib/swagger` is not used (its latest release still requires Fiber v2). The page is hand-rolled in `docs_controller.go` and loads Swagger UI's assets from unpkg, so the docs page needs internet access even though the API does not.

`web.swagger` turns it off (`WEB_SWAGGER=false`). When false, `Bootstrap` leaves `RouteConfig.DocsController` **nil** and neither route is registered — nil rather than a boolean, so the routes cannot be enabled without something to serve them. `NewViper` calls `SetDefault("web.swagger", true)` because `GetBool` answers false for an absent key, which would make a pre-existing `config.json` silently lose the docs.

**`README.md` is a third surface that goes stale**, not just a front page: it carries its own endpoint table, authorization matrix, and roadmap. A route change touches `route.go`, `docs/openapi.yaml`, *and* that table.

### Language

Identifiers and schema use the project's Indonesian domain vocabulary (`satuan`, `ruang`, `kartu_stok`, `berlaku_dari`) — keep it; don't translate to English when adding tables or fields. Go comments are English and explain *why*, not what. `README.md` and commit subjects are Indonesian (`feat: modul user & role, lingkungan Docker, dan Swagger di root`); `CLAUDE.md` and code comments are English.
