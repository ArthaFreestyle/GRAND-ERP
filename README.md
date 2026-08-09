# GRAND-ERP

Backend ERP dengan fokus pada persediaan, pembelian, dan penjualan. Ditulis dengan Go + Fiber v3 di atas PostgreSQL, tanpa ORM.

> **Status: tahap scaffold.** Seluruh lapisan arsitektur sudah ada dan kompilasi bersih, skema database lengkap sudah termigrasi, tetapi baru satu modul yang punya kode Go: `ruang`. Lihat [Status & Roadmap](#status--roadmap).

## Stack

| Komponen | Pilihan |
|---|---|
| Bahasa | Go 1.25 |
| HTTP | [Fiber v3](https://github.com/gofiber/fiber) |
| Database | PostgreSQL via `database/sql` + [pgx/v5](https://github.com/jackc/pgx) — **tanpa ORM** |
| Cache | Redis (disiapkan untuk sesi captcha ber-TTL) |
| Config | viper (`config.json` + override environment) |
| Log | logrus (JSON) |
| Migrasi | [golang-migrate](https://github.com/golang-migrate/migrate) |
| Validasi | go-playground/validator |

**Go 1.25 wajib** — Fiber v3 menolak dibangun di bawah versi itu.

## Persiapan

### 1. Prasyarat

- Go 1.25 atau lebih baru
- PostgreSQL 17 (versi 14+ semestinya cukup)
- Redis
- CLI `migrate`, **harus dibangun dengan driver postgres**:

```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

Tanpa tag itu, `migrate` gagal dengan `unknown driver postgres`.

### 2. Konfigurasi

```bash
cp config.example.json config.json
```

Isi `database.password` di `config.json`. File ini masuk `.gitignore` dan tidak pernah ikut ter-commit.

Semua kunci bisa ditimpa environment variable dengan mengganti `.` jadi `_`:

```bash
DATABASE_HOST=db.internal DATABASE_PASSWORD=rahasia WEB_PORT=8080 go run ./cmd/web
```

`log.level` mengikuti level logrus: `4` warn, `5` debug, `6` trace.

### 3. Database

```bash
createdb grand_erp

export DSN="postgres://postgres:PASSWORD@localhost:5432/grand_erp?sslmode=disable"
migrate -path db/migrations_postgres -database "$DSN" up

psql "$DSN" -f db/seeder_postgres/001_ruang.sql
```

Seeder ditulis idempoten (`ON CONFLICT DO NOTHING`), aman dijalankan ulang.

### 4. Jalankan

```bash
go run ./cmd/web      # HTTP server, default :3000
go run ./cmd/worker   # background worker (belum ada job)
```

Cek: `curl http://localhost:3000/health`

## Perintah

```bash
go build ./...                                  # build semua
go vet ./...
gofmt -l .                                      # daftar file belum terformat

go test ./...                                   # semua test
go test ./internal/usecase -run TestRuangCreate # satu test
go test -race ./...
```

Migrasi:

```bash
migrate -path db/migrations_postgres -database "$DSN" up
migrate -path db/migrations_postgres -database "$DSN" down 1
migrate -path db/migrations_postgres -database "$DSN" force <versi>   # bersihkan state dirty
migrate create -ext sql -dir db/migrations_postgres -seq <nama>       # pasangan up/down baru
```

## Struktur

```
cmd/web/              entrypoint HTTP
cmd/worker/           entrypoint background worker
internal/config/      viper, logrus, postgres, redis, fiber, validator, Bootstrap
internal/entity/      struct domain, dipetakan ke tabel
internal/model/       DTO request/response, error domain, converter/
internal/repository/  akses data — semua SQL ada di sini
internal/usecase/     business logic, validasi, batas transaksi
internal/delivery/    handler Fiber + routing
db/migrations_postgres/
db/seeder_postgres/
docs/openapi.yaml     kontrak API
```

### Aturan lapisan

Ketergantungan mengalir satu arah: `delivery → usecase → repository → PostgreSQL/Redis`.

- **Semua SQL ada di `internal/repository`.** Handler tidak pernah menyentuh `database/sql`.
- **Transaksi dimiliki `internal/usecase`.** Repository menerima `DBTX` (dipenuhi `*sql.DB` maupun `*sql.Tx`) sehingga usecase yang memutuskan batas transaksi.
- **`internal/entity` tidak pernah melewati usecase.** Delivery hanya melihat tipe `internal/model`.
- **Usecase tidak meng-import Fiber.** Kesalahan dikembalikan sebagai error domain (`model.NotFound`, `model.Conflict`, …); `statusForKind` di `internal/config/fiber.go` satu-satunya tempat kind diterjemahkan jadi status HTTP.
- **Semua wiring di `config.Bootstrap`** (`internal/config/app.go`).

Menambah modul: ikuti urutan `ruang` — migrasi → entity → model → converter → repository → usecase → controller → daftarkan di `route.RouteConfig` → wire di `config.Bootstrap` → perbarui `docs/openapi.yaml`.

## Model data persediaan

Skema lengkap ada di migrasi `000002`–`000008`. Beberapa jaminan ditegakkan database, bukan aplikasi:

- **`kartu_stok` satu-satunya sumber kebenaran stok dan nilai persediaan.** Tidak ada kolom stok di tabel master.
- **Append-only, dijaga trigger.** `UPDATE`, `DELETE`, dan `TRUNCATE` ditolak. Koreksi dilakukan lewat baris pembalik yang mengisi `id_kartu_stok_asal`.
- **Trigger yang menghitung saldo, bukan aplikasi.** `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, dan `nilai_akhir` ditimpa saat insert. Aplikasi hanya mengirim arah pergerakan, `nilai_masuk`, dan kolom referensi.
- **Rata-rata bergerak**: barang masuk menggeser harga pokok, barang keluar tidak pernah. Stok nol memaksa nilai persediaan tepat nol.
- Saldo dipartisi per `(id_barang, id_ruang)` dan diurutkan pakai `id`, bukan tanggal. Insert mengambil `pg_advisory_xact_lock` pada pasangan itu.
- Trigger menolak stok negatif dan posting ke `periode` berstatus `TUTUP`.
- Dokumen menyimpan snapshot (harga, faktor konversi, HPP); master menyimpan aturan berjalan. Retur menyalin harga pokok dari baris dokumen asal.
- Stok hanya bergerak saat posting, tidak saat draft.
- `status_pembayaran` di `penjualan` dan `pembelian` adalah **cache**, selalu dihitung ulang dari alokasi dan retur berstatus POSTED.

Penjelasan lengkap termasuk aturan yang masih harus divalidasi di aplikasi ada di [CLAUDE.md](CLAUDE.md).

## API

Kontrak di [`docs/openapi.yaml`](docs/openapi.yaml). Setiap perubahan route, request, atau response wajib tercermin di sana pada commit yang sama.

Endpoint yang sudah ada:

| Method | Path | Keterangan |
|---|---|---|
| `GET` | `/health` | Liveness probe |
| `GET` | `/api/v1/ruang` | List, mendukung `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/ruang` | Create |
| `GET` | `/api/v1/ruang/{id}` | Get by id |

Semua respons memakai satu amplop:

```json
{
  "data": { "...": "..." },
  "paging": { "page": 1, "size": 20, "total_item": 5, "total_page": 1 },
  "errors": "ruang not found",
  "validation_errors": { "NamaRuang": "required" }
}
```

## Status & Roadmap

Sudah ada:

- Skema database lengkap: master data, kartu stok beserta trigger saldo, pembelian, penjualan, piutang, utang, pemakaian, mutasi, stok opname
- Wiring aplikasi penuh, graceful shutdown, penanganan error terpusat
- Satu modul contoh: `ruang`

Belum ada:

- Autentikasi, sesi, dan middleware otorisasi
- Captcha (Redis sudah terhubung tapi belum dipakai)
- Modul Go untuk seluruh tabel persediaan dan transaksi
- Validasi tingkat aplikasi yang tidak bisa ditegakkan database (kuota retur kumulatif, penjumlahan alokasi biaya angkut, plafon kredit, dan lainnya — didaftar di CLAUDE.md)
- Job rekonsiliasi harian rantai saldo kartu stok
- Test
