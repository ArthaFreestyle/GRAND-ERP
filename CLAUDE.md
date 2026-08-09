# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Status

Scaffold stage. Every layer exists and compiles, but only one vertical slice is implemented: **`ruang`** (create / get / list). Use it as the template when adding a module — copy the file set, don't invent a new shape.

- Module path: `Arthafreestyle/ERP` (no domain prefix); internal imports are `Arthafreestyle/ERP/internal/...`
- Go 1.25.0 — required by Fiber v3.4.0, which refuses to build on 1.24
- The full inventory/sales/purchasing schema exists as migrations `000002`–`000007`, but has **no Go layers yet** — see "Inventory data model" below
- Not built yet: auth/session, captcha (Redis is wired but unused), middleware, any worker job, tests

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
go test ./internal/usecase -run TestRuangCreate      # one test (regex match)
go test -v -race ./...                               # verbose + race detector
```

Config comes from `config.json` in the working directory, and **any key can be overridden by an env var** with `.` replaced by `_` — `DATABASE_HOST`, `WEB_PORT`, `REDIS_PASSWORD`. `config.json` is gitignored; the tracked file is `config.example.json`, so **any new config key must be added to the example too** or a fresh clone silently loses it.

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

Seed data lives in `db/seeder_postgres/` (starting with `ruang`) and is applied separately from migrations. Seeders are written to be idempotent (`ON CONFLICT DO NOTHING`).

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

Follow the `ruang` slice, in this order: migration in `db/migrations_postgres/` (most inventory tables already exist — check first) → `entity` → `model` DTOs → `model/converter` → `repository` (methods take `DBTX`) → `usecase` (validate, own the transaction) → `delivery/http` controller → register in `route.RouteConfig` → wire in `config.Bootstrap` → update `docs/openapi.yaml`.

### PostgreSQL specifics

Since there is no ORM, these are hand-written every time — get them right:

- Placeholders are `$1, $2, …`, not `?`.
- `LastInsertId()` is unsupported by the driver. To get a generated key, use `RETURNING id` with `QueryRowContext(...).Scan(&id)`.
- Always use the `…Context` variants and thread the `context.Context` through. Note the limit: Fiber v3's `c.Context()` defaults to `context.Background()` and is **not** cancelled when the client disconnects, so a slow query is not aborted for free — attach an explicit timeout where one matters.
- Identifiers fold to lowercase unless double-quoted; keep table and column names lowercase `snake_case` so quoting is never needed.
- Use `TIMESTAMPTZ` (not `TIMESTAMP`) for anything representing a real point in time.
- `updated_at` is maintained by the `set_updated_at()` trigger installed in migration `000001`; reuse it for new tables rather than setting the column from Go.

## API contract

`docs/openapi.yaml` is the contract. Update it in the same change as any route, request, or response shape change in `internal/delivery` and `internal/model`.
