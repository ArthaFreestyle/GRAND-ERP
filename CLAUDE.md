# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Early stage. Master data is implemented; nothing transactional is. Copy an existing slice when adding a module — don't invent a new shape.

- Implemented: **`satuan`**, **`ekspedisi`**, **`supplier`**, **`pelanggan`**, **`role`**, **`user`** (create / get / list / patch) and **`ruang`** (create / get / list, no patch)
- Use **`supplier`** as the template for a plain master slice: it is the one with every ordinary concern at once — nullable unique `kode`, PATCH presence semantics, and a `LEFT JOIN` in the list query
- Use **`user`** as the template when a slice writes more than one table — it is the only one that does: a user and its `user_role` grants are written in one transaction, and it is where `Optional[[]int64]`, bcrypt hashing, and the `Touch` pattern live. See "Users and roles" below
- Module path: `Arthafreestyle/ERP` (no domain prefix); internal imports are `Arthafreestyle/ERP/internal/...`
- Go 1.25.0 — required by Fiber v3.4.0, which refuses to build on 1.24
- The full inventory/sales/purchasing schema exists as migrations `000002`–`000008`, but has **no Go layers yet** — see "Inventory data model" below
- Not built yet: auth/session — `users` exists and holds bcrypt hashes, but nothing verifies one, so there is still no actor and every `created_by`/`updated_by` is written as `NULL`; captcha (Redis is wired but unused), authorization middleware, any worker job, `product`, `periode`
- **`/api/v1/user` is unauthenticated.** Until middleware exists, anyone who can reach the server can create a user and grant it `SUPERADMIN`. Don't expose the server to an untrusted network

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
gofmt -l .                       # list unformatted files

go test ./...                                        # all tests
go test ./internal/usecase/...                       # one package
go test ./internal/usecase -run TestSatuanCreate     # one test (regex match)
go test -v -race ./...                               # verbose + race detector
```

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

`truncateMaster` in `internal/usecase/main_test.go` deletes in dependency order: master tables, then `user_role`, then `users`, then `role`. Add new tables to that list, on the correct side — `users` has to come after anything whose `created_by` references it.

```bash
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/grand_erp_test?sslmode=disable'
migrate -path db/migrations_postgres -database "$TEST_DATABASE_URL" up
go test ./...
```

Everything outside `internal/usecase` is a pure unit test and needs no database.

Config comes from `config.json` in the working directory, and **any key can be overridden by an env var** with `.` replaced by `_` — `DATABASE_HOST`, `WEB_PORT`, `REDIS_PASSWORD`. `config.json` is gitignored; the tracked file is `config.example.json`, so **any new config key must be added to the example too** or a fresh clone silently loses it.

`NewViper` **panics** when `config.json` is missing — env vars alone are not enough to boot. That is why the Dockerfile copies `config.example.json` to `config.json` inside the image, and compose then overrides the environment-dependent keys. The ones compose does not set (`app.name`, `captcha.ttl_seconds`, `database.pool.*`) fall back to the baked example's values. `.dockerignore` excludes the real `config.json` so local credentials are never baked into a layer.

A new config key therefore has three homes, not one: `config.example.json`, and — if a container needs a non-default value — the `web` and `worker` `environment:` blocks in `docker-compose.yml`.

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

Seed data lives in `db/seeder_postgres/` (`001_ruang.sql`, `002_satuan.sql`, `003_role.sql`) and is applied separately from migrations. Seeders are written to be idempotent (`ON CONFLICT DO NOTHING`), and their conflict target must name the index expression — `ON CONFLICT (lower(kode))`, not `(kode)` — since migration `000009` moved master uniqueness onto `lower(...)`.

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

The schema is installed but almost **no Go code touches it yet** — only `ruang` has a slice. Read these invariants before writing any inventory usecase; several are enforced by the database and will reject wrong code at runtime.

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

- `product_satuan` must include the base unit with `faktor = 1`
- cumulative return qty must not exceed the source document's qty
- `jumlah_koli` across details must equal the header's `total_koli` before a purchase may post; offer a "bagi rata" button that splits `total_koli` proportionally to `qty_dasar`
- `alokasi_biaya` must sum exactly to `biaya_angkut`; push the rounding remainder onto the line with the largest `jumlah_koli`
- posted documents must reject detail edits
- cancelling a posted document writes reversing `kartu_stok` rows with `id_kartu_stok_asal` set and HPP copied from the original
- allocation must not exceed the payment amount or the document's remaining balance, on either the receivable or the payable side
- cancelled documents must not accept allocations
- credit sales must respect `plafon_kredit`
- a payment, all its allocations, and every touched `status_pembayaran` must be written in one database transaction

The daily reconciliation job over the balance chain (section F) is also not built.

### Adding a module

Follow the `supplier` slice, in this order: migration in `db/migrations_postgres/` (most inventory tables already exist — check first) → `entity` → `model` DTOs → `model/converter` → `repository` (methods take `DBTX`) → `usecase` (validate, own the transaction) → `delivery/http` controller → register in `route.RouteConfig` → wire in `config.Bootstrap` → update `docs/openapi.yaml`.

If the slice writes more than one table, follow `user` instead — it is the worked example of a usecase holding two repositories and committing both tables in one transaction.

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
- Tags on an `Optional` field must lead with `omitempty`, and each instantiation must be registered in `config.NewValidator` — otherwise its validation tags are silently ignored. Registered today: `Optional[string]`, `Optional[bool]`, `Optional[[]int64]`.
- A collection field works the same way, with the states meaning replace rather than set: absent leaves it alone, `[]` empties it, a list replaces it wholesale. `dive` does reach elements through the custom type func (`TestValidatorDivesIntoOptionalSlice` pins that), so `dive,gt=0` on an `Optional[[]int64]` is really enforced.

## API contract

`docs/openapi.yaml` is the contract. Update it in the same change as any route, request, or response shape change in `internal/delivery` and `internal/model`.

It is also a **build input**, not just documentation: `docs/docs.go` pulls it in with `go:embed` so the server can serve Swagger UI at `/` and the spec at `/openapi.yaml`. Two consequences:

- Dropping `docs/` from the Docker build context **fails compilation** (`pattern openapi.yaml: no matching files found`) rather than merely losing the docs page. `.dockerignore` deliberately does not exclude it and the Dockerfile copies it in.
- A malformed `openapi.yaml` is still served happily — `go:embed` copies bytes and does not parse YAML. `TestEmbeddedSpecIsTheRealContract` only checks the asset arrived and is not empty.

`gofiber/contrib/swagger` is not used: its latest release (v1.3.0) still requires Fiber v2. The page is hand-rolled in `internal/delivery/http/docs_controller.go` and loads Swagger UI's assets from unpkg, so the docs page needs internet access even though the API does not.

`web.swagger` turns it off (`WEB_SWAGGER=false`). When false, `config.Bootstrap` leaves `RouteConfig.DocsController` nil and **neither route is registered** — nil rather than a boolean so the routes cannot be enabled without something to serve them. `NewViper` calls `SetDefault("web.swagger", true)` because `GetBool` answers false for an absent key, which would otherwise make a pre-existing `config.json` silently lose the docs.
