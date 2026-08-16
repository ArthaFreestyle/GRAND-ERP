# GRAND-ERP

Backend ERP dengan fokus pada persediaan, pembelian, dan penjualan. Ditulis dengan Go + Fiber v3 di atas PostgreSQL, tanpa ORM.

> **Status: master data, pengguna, produk, siklus barang masuk-keluar dari supplier, perpindahan antar ruang, pemakaian internal, dan nota penjualan berjalan.** Delapan belas modul sudah punya kode Go lengkap dari migrasi sampai OpenAPI — `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `unit_kerja`, `ruang`, `role`, `user`, `product` (beserta `product_satuan` dan `product_harga_jual`), `pembelian`, `penerimaan_susulan`, `retur_pembelian`, `pembayaran_utang`, `mutasi`, `pemakaian`, `penjualan`, `periode` (tutup buku bulanan), dan `dokumen` (lampiran berkas lintas modul, sekaligus **job pertama di `cmd/worker`**). **Isu #12 (`unit_kerja`, wewenang bertempat) punya kelima fase wajibnya plus bagian opsional fase 6** — lokasi organisasi yang jadi induk setiap `ruang`, `user_role` membawa `id_unit_kerja` sehingga satu role bisa dipegang di lebih dari satu unit, sebuah sesi mengotorisasi sebagai satu grant aktif lewat `POST /api/v1/auth/switch-context`, `id_ruang` pada `pembelian`/`mutasi` divalidasi terhadap unit aktif itu, dan bacaan (`Get`/`List` pada `ruang`, `pembelian`, `penerimaan-susulan`, `retur-pembelian`, `mutasi`, serta `GET /product/{id}/stok`) disaring oleh unit aktif yang sama — `pemakaian` (isu #9) dan `penjualan` (isu #10) sengaja tidak ikut disaring, karena kedua isu itu sendiri tidak memintanya. Lihat [catatan di bawah](#status--roadmap). **`pembelian` adalah dokumen transaksi pertama, dan yang pertama menulis ke `kartu_stok`** — mesin posting dan generator nomor dokumennya dipakai ulang seluruh modul transaksi berikutnya. **`mutasi` yang pertama menulisnya ke dua arah sekaligus**, dan bersamanya datang bacaan saldo pertama atas `kartu_stok` ([stok per ruang](#stok-per-ruang-bacaan-pertama-atas-kartu-stok)). **`pemakaian` adalah dokumen kelima yang menulis `kartu_stok`, dan yang pertama mengeluarkan barang tanpa lawan transaksi sama sekali** — lihat [Pemakaian internal](#pemakaian-internal-permintaan-persetujuan-berjenjang-dan-posting). **`penjualan` adalah dokumen keenam, dan yang pertama mengeluarkan barang ke pihak luar dengan uang di sisi lain — awal sisi piutang** — lihat [Penjualan](#penjualan-nota-keluar-hpp-dari-trigger-dan-awal-sisi-piutang). **Purchase order sengaja tidak ada**; penggantinya adalah [riwayat harga beli](#riwayat-harga-beli-pengganti-purchase-order), yang terkumpul sendiri dari pembelian yang sudah diposting. Retur penjualan, penerimaan pembayaran (cerminan `pembayaran_utang` di sisi piutang), dan stok opname skemanya sudah termigrasi tetapi belum punya lapisan Go. Lihat [Status & Roadmap](#status--roadmap).

> [!WARNING]
> Seeder memasang superadmin bawaan **`admin` / `admin12345`**, password yang tercatat di repositori ini. Itu kredensial untuk mesin sendiri. Ganti atau nonaktifkan sebelum server bisa dijangkau orang lain — lihat [Autentikasi](#autentikasi).

## Stack

| Komponen | Pilihan |
|---|---|
| Bahasa | Go 1.25 |
| HTTP | [Fiber v3](https://github.com/gofiber/fiber) |
| Database | PostgreSQL via `database/sql` + [pgx/v5](https://github.com/jackc/pgx) — **tanpa ORM** |
| Cache | Redis (disiapkan untuk sesi captcha ber-TTL, belum dipakai) |
| Config | viper (`config.json` + override environment) |
| Log | logrus (JSON) |
| Migrasi | [golang-migrate](https://github.com/golang-migrate/migrate) |
| Validasi | go-playground/validator |

**Go 1.25 wajib** — Fiber v3 menolak dibangun di bawah versi itu.

## Jalankan dengan Docker

Cara tercepat, dan tidak butuh Go, PostgreSQL, Redis, maupun CLI `migrate` terpasang di mesin:

```bash
cp .env.example .env          # opsional, semua nilai punya default
docker compose up -d --build

curl http://127.0.0.1:3000/health
```

Lalu buka **<http://127.0.0.1:3000>** — root menyajikan Swagger UI untuk seluruh API.

> [!IMPORTANT]
> Pakai **`127.0.0.1`, bukan `localhost`.** Compose mempublikasikan port ke `0.0.0.0`, yang hanya IPv4, sedangkan `localhost` di banyak mesin Windows dan macOS diselesaikan ke `::1` (IPv6) lebih dulu. Akibatnya `http://localhost:3000` bisa langsung reset koneksi padahal containernya sehat — sudah terjadi dan bukan dugaan. `curl http://127.0.0.1:3000/health` membedakan keduanya dengan cepat.

Itu menaikkan PostgreSQL 17, Redis 7, menjalankan migrasi, memasang seeder, lalu menjalankan server HTTP dan worker. Urutannya **ditegakkan**, bukan diharapkan — `postgres` harus lolos healthcheck sebelum `migrate` jalan, `migrate` harus selesai sebelum `seed`, dan `web` baru mulai setelah keduanya beres. Tanpa itu aplikasi bisa naik lebih dulu daripada skemanya lalu langsung mati, karena `NewDatabase` melakukan ping saat boot dan `Fatal` kalau gagal.

| Service | Peran |
|---|---|
| `postgres` | PostgreSQL 17, data di volume `postgres-data` |
| `redis` | Redis 7, persistence dimatikan — captcha memang state ber-TTL |
| `migrate` | Sekali jalan: migrasi ke `grand_erp`, lalu keluar |
| `migrate-test` | Sekali jalan: migrasi ke `grand_erp_test` untuk test |
| `seed` | Sekali jalan setelah `migrate`: memasang `db/seeder_postgres/` |
| `web` | Server HTTP di `:3000`, dengan healthcheck ke `/health` |
| `worker` | Worker latar; menjalankan pembersihan berkas yatim sekali sehari |

Lampiran berkas disimpan di volume `dokumen-data`, yang **dipasang di `web` dan `worker` sekaligus** — `web` yang menulis, `worker` yang menghapus. Kalau hanya salah satunya memasangnya, pembersihannya diam-diam tidak melakukan apa-apa. Tanpa volume sama sekali, isinya hilang setiap `up --build` sementara barisnya di tabel tetap ada.

> [!IMPORTANT]
> **PostgreSQL dipublikasikan di host port `5433`, bukan `5432`.** Mesin pengembangan biasanya sudah menjalankan PostgreSQL sendiri di 5432, dan memakai port yang sama di sini gagal bind dengan `address already in use`. Di dalam jaringan compose alamatnya tetap `postgres:5432`. Ubah lewat `POSTGRES_HOST_PORT` di `.env` kalau 5432 memang bebas.

Perintah harian:

```bash
docker compose logs -f web              # ikuti log server
docker compose restart web              # setelah mengubah env
docker compose up -d --build web        # setelah mengubah kode Go
docker compose down                     # stop, data tetap ada
docker compose down -v                  # stop dan buang data, termasuk grand_erp_test
```

Migrasi manual dipakai ulang service `migrate` yang sama, jadi driver postgres-nya sudah pasti ada. Perhatikan hostnya `postgres` dan portnya `5432` — perintah ini berjalan **di dalam** jaringan compose, bukan dari host:

```bash
# turunkan satu versi
docker compose run --rm migrate \
  -path=/migrations \
  -database "postgres://postgres:postgres@postgres:5432/grand_erp?sslmode=disable" \
  down 1

# bersihkan state dirty
docker compose run --rm migrate \
  -path=/migrations \
  -database "postgres://postgres:postgres@postgres:5432/grand_erp?sslmode=disable" \
  force 9
```

Sesuaikan `postgres:postgres` kalau `POSTGRES_USER`/`POSTGRES_PASSWORD` diubah di `.env`.

`docker compose down -v` juga satu-satunya cara memicu ulang `docker/initdb/` — berkas di sana hanya dijalankan saat direktori data PostgreSQL masih kosong.

**Konfigurasi di dalam container.** `config.NewViper` panik kalau `config.json` tidak ada, sedangkan `config.json` gitignored karena berisi kredensial. Jadi image menyalin `config.example.json` menjadi `config.json`, lalu compose menimpa kunci yang memang bergantung lingkungan lewat environment variable (`database.host` → `DATABASE_HOST`).

Yang **tidak** ditimpa compose — `app.name`, `captcha.ttl_seconds`, dan ketiga kunci `database.pool.*` — memakai nilai dari `config.example.json` yang terbangun ke dalam image. `dokumen.storage_path` justru ditimpa di kedua service dan harus tetap sama di keduanya; direktorinya dibuat di dalam image dan dimiliki user non-root, karena Docker menyalin kepemilikan itu saat volume bernama pertama kali dibuat. Kalau salah satunya perlu berbeda per lingkungan, tambahkan ke blok `environment:` service `web` dan `worker`.

`.dockerignore` sengaja mengecualikan `config.json` supaya kredensial lokal tidak ikut terbangun ke dalam image.

Image aplikasi dibangun dua tahap: `golang:1.25-alpine` untuk kompilasi, lalu `alpine:3.21` berisi binernya saja, berjalan sebagai user non-root. `CGO_ENABLED=0` membuat binernya statis — pgx murni Go, jadi tidak ada libpq yang perlu diurus. `Dockerfile` punya dua target, `web` dan `worker`, yang berbagi seluruh layer dan hanya berbeda pada perintah dan healthcheck.

## Persiapan tanpa Docker

Tetap didukung penuh, dan tetap cara yang paling nyaman untuk menjalankan `go run` sambil menyunting kode.

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

Isi `database.password` **dan `jwt.secret`** di `config.json`. File ini masuk `.gitignore` dan tidak pernah ikut ter-commit — karena itu **setiap kunci config baru wajib ditambahkan juga ke `config.example.json`**, kalau tidak clone baru kehilangan kunci itu tanpa suara.

> [!IMPORTANT]
> Contohnya menunjuk **`127.0.0.1:5433`**, yaitu PostgreSQL milik compose, karena itu kombinasi yang paling sering dipakai: `go run` di host sambil databasenya dibiarkan jalan di Docker. **Kalau Anda memasang PostgreSQL sendiri**, kembalikan `database.port` ke `5432` — dan ingat instalasi lokal biasanya memang sudah memegang port itu, yang justru alasan compose memilih 5433.
>
> Host-nya ditulis `127.0.0.1`, bukan `localhost`, dan itu bukan gaya penulisan. Di Windows dengan Docker Desktop, `localhost` bisa resolve ke IPv6 `::1` lalu menggantung sampai timeout alih-alih menolak, sehingga gejalanya menyerupai database yang mati. Sama berlakunya untuk `redis.host`.

`jwt.secret` kosong di contohnya dan **tidak punya default**, jadi server berhenti saat boot sampai diisi, minimal 32 karakter:

```bash
openssl rand -base64 48
```

Semua kunci bisa ditimpa environment variable dengan mengganti `.` jadi `_`:

```bash
DATABASE_HOST=db.internal DATABASE_PASSWORD=rahasia WEB_PORT=8080 go run ./cmd/web
```

`log.level` mengikuti level logrus: `3` warn, `4` info, `5` debug, `6` trace.

### 3. Database

Langkah ini untuk PostgreSQL yang Anda pasang sendiri — **sesuaikan portnya dengan yang dipakai `config.json`**. Kalau databasenya dibiarkan jalan di compose, seluruh blok ini tidak perlu: `docker compose up` sudah memigrasi dan menyemai keduanya, `grand_erp` maupun `grand_erp_test`.

```bash
createdb grand_erp

export DSN="postgres://postgres:PASSWORD@127.0.0.1:5432/grand_erp?sslmode=disable"
migrate -path db/migrations_postgres -database "$DSN" up

psql "$DSN" -f db/seeder_postgres/001_ruang.sql
psql "$DSN" -f db/seeder_postgres/002_satuan.sql
psql "$DSN" -f db/seeder_postgres/003_role.sql
psql "$DSN" -f db/seeder_postgres/004_superadmin.sql
```

`003_role.sql` memasang tiga role yang berlaku sekarang — `SUPERADMIN`, `CASHIER`, `INVENTARIS`. Tanpa itu `user_role` tidak bisa diisi dan setiap user berakhir tanpa role. `004_superadmin.sql` memasang user pertama; tanpa itu tidak ada yang bisa login sehingga tidak ada yang bisa membuat user — lihat [Autentikasi](#autentikasi).

Seeder ditulis idempoten (`ON CONFLICT DO NOTHING`), aman dijalankan ulang. Target konflik harus menyebut ekspresi indeks — `ON CONFLICT (lower(kode))`, bukan `(kode)` — karena migrasi `000009` memindahkan keunikan master ke `lower(...)`.

> [!NOTE]
> Service `seed` di `docker-compose.yml` menyebut setiap berkas seeder satu per satu, bukan memindai direktorinya. **Seeder baru harus didaftarkan di sana**, kalau tidak ia tidak akan pernah jalan di lingkungan Docker.

### 4. Jalankan

```bash
go run ./cmd/web      # HTTP server, default :3000
go run ./cmd/worker   # background worker: pembersihan berkas yatim
```

Keduanya membaca `dokumen.storage_path` dari config yang sama, dan **harus menunjuk direktori yang sama** — `web` yang menulis lampiran, `worker` yang menghapus yang tidak pernah tertempel. Direktorinya dibuat sendiri saat boot; kalau tidak bisa ditulisi, prosesnya berhenti di situ, bukan di unggahan pertama.

Cek: `curl http://127.0.0.1:3000/health`, lalu buka <http://127.0.0.1:3000> untuk Swagger UI.

## Perintah

```bash
go build ./...                                     # build semua
go vet ./...
gofmt -l .                                         # daftar file belum terformat

go test ./...                                      # semua test
go test ./internal/usecase/...                     # satu paket
go test ./internal/usecase -run TestSupplierPatch  # cocok regex
go test -v -race ./...
```

### Test

Test di `internal/usecase` berjalan melawan **PostgreSQL sungguhan** dan melewatkan dirinya sendiri kalau `TEST_DATABASE_URL` tidak menunjuk database bekas. Yang mereka buktikan hidup di database, bukan di Go — stabilitas paginasi saat nama kembar, escaping wildcard `ILIKE`, banyak baris berbagi `kode = NULL` di bawah indeks unik, dan `NUMERIC` yang bulak-balik tanpa berubah. Mock hanya akan menyetujui query yang salah.

Dengan compose, databasenya sudah disiapkan: `docker/initdb/` membuat `grand_erp_test` dan service `migrate-test` memasang skemanya. Jadi cukup arahkan `TEST_DATABASE_URL` ke sana — **port 5433**, karena itu port yang dipublikasikan ke host:

```bash
docker compose up -d
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5433/grand_erp_test?sslmode=disable'
go test ./...
```

Tanpa compose, siapkan sendiri lalu migrasikan:

```bash
createdb grand_erp_test
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/grand_erp_test?sslmode=disable'
migrate -path db/migrations_postgres -database "$TEST_DATABASE_URL" up
go test ./...
```

Test membersihkan tabel master sendiri, tapi **tidak** membuat skemanya — migrasikan dulu. Di luar `internal/usecase` semuanya unit test murni dan tidak butuh database, jadi `go test ./...` tetap hijau di mesin tanpa PostgreSQL. Test yang butuh database melewatkan dirinya sendiri, jadi hijau tanpa `TEST_DATABASE_URL` **bukan** berarti test itu lulus — jalankan dengan `-v` kalau ingin melihat mana yang di-skip.

### Migrasi

```bash
migrate -path db/migrations_postgres -database "$DSN" up
migrate -path db/migrations_postgres -database "$DSN" down 1
migrate -path db/migrations_postgres -database "$DSN" force <versi>   # bersihkan state dirty
migrate create -ext sql -dir db/migrations_postgres -seq <nama>       # pasangan up/down baru
```

Migrasi `000001` hanya membuat fungsi trigger bersama `set_updated_at()` — setiap tabel yang punya kolom `updated_at` memakai ulang fungsi itu.

## Struktur

```
cmd/web/              entrypoint HTTP
cmd/worker/           entrypoint background worker
internal/config/      viper, logrus, postgres, redis, fiber, validator, Bootstrap
internal/entity/      struct domain, dipetakan ke tabel
internal/model/       DTO request/response, error domain, Optional[T], converter/
internal/repository/  akses data — semua SQL ada di sini
internal/usecase/     business logic, validasi, batas transaksi
internal/delivery/    handler Fiber + routing
db/migrations_postgres/
db/seeder_postgres/
docker/initdb/        dijalankan sekali saat data PostgreSQL masih kosong
docs/openapi.yaml     kontrak API — sekaligus input build, ditanam lewat go:embed
docs/docs.go          go:embed openapi.yaml supaya biner bisa menyajikannya
Dockerfile            dua tahap, dua target: web dan worker
docker-compose.yml    postgres, redis, migrate, seed, web, worker
.env.example          nilai untuk compose; salin ke .env
config.example.json   template config aplikasi; salin ke config.json
```

### Aturan lapisan

Ketergantungan mengalir satu arah: `delivery → usecase → repository → PostgreSQL/Redis`.

- **Semua SQL ada di `internal/repository`.** Handler tidak pernah menyentuh `database/sql`.
- **Transaksi dimiliki `internal/usecase`.** Repository menerima `DBTX` (dipenuhi `*sql.DB` maupun `*sql.Tx`) sehingga usecase yang memutuskan batas transaksi.
- **`internal/entity` tidak pernah melewati usecase.** Delivery hanya melihat tipe `internal/model`.
- **Usecase tidak meng-import Fiber.** Kesalahan dikembalikan sebagai error domain (`model.Invalid`, `model.NotFound`, `model.Conflict`, …); `statusForKind` di `internal/config/fiber.go` satu-satunya tempat kind diterjemahkan jadi status HTTP. Handler cukup `return err`; `ErrorHandler` yang memformat, membuka `validator.ValidationErrors` jadi `validation_errors`, dan hanya mencatat 5xx. Error biasa jadi 500 bergeneric message — detail internal tidak pernah sampai ke klien.
- **Semua wiring di `config.Bootstrap`** (`internal/config/app.go`).

Menambah modul: ikuti slice **`supplier`** — satu-satunya master yang memuat semua perkara biasa sekaligus (`kode` unik tapi nullable, semantik PATCH, dan `LEFT JOIN` di query list). Urutannya: migrasi → entity → model → converter → repository (metode menerima `DBTX`) → usecase → controller → daftarkan di `route.RouteConfig` → wire di `config.Bootstrap` → perbarui `docs/openapi.yaml`.

Kalau modulnya menulis lebih dari satu tabel, ikuti **`user`** — satu-satunya yang begitu, dan contoh nyata usecase memegang dua repository serta menulis dua tabel dalam satu transaksi.

**Master data tidak punya `DELETE`.** Setiap tabel master dirujuk tabel transaksi, jadi menghapus baris terpakai entah gagal di foreign key atau merusak jejak audit. Pensiunkan baris dengan `is_aktif = false`.

### Semantik PATCH

Pointer tidak bisa membedakan "field tidak dikirim" dari "field diisi null", dan `COALESCE($n, col)` diam-diam mempertahankan nilai lama untuk keduanya — operator jadi tidak pernah bisa mengosongkan nomor telepon yang salah ketik. Karena itu setiap update parsial memakai `model.Optional[T]`, yang `UnmarshalJSON`-nya mencatat kehadiran kunci:

- Kolom nullable: `col = CASE WHEN $n::BOOLEAN THEN $m ELSE col END`, flag-nya dari `Optional.Present`.
- Kolom `NOT NULL`: `COALESCE` sudah benar, dan `null` eksplisit ditolak dengan `model.Invalid`.
- `UPDATE ... RETURNING` yang menyediakan response; `sql.ErrNoRows` berarti id tidak ada. Jangan `SELECT` dulu untuk memeriksa — dua query, tetap rawan balapan.
- `id`, `created_at`, dan `created_by` tidak pernah muncul di DTO update. Controller bind body dulu, baru menimpa `ID` dari path.
- Tag pada field `Optional` harus dimulai `omitempty`, dan setiap instansiasi wajib didaftarkan di `config.NewValidator` — kalau tidak, tag validasinya diabaikan tanpa suara.

### Catatan PostgreSQL

Tanpa ORM, hal-hal ini ditulis tangan setiap kali:

- Placeholder `$1, $2, …`, bukan `?`. `LastInsertId()` tidak didukung driver — pakai `RETURNING id` dengan `QueryRowContext(...).Scan(&id)`.
- Selalu varian `…Context`. Perhatikan batasnya: `c.Context()` Fiber v3 default `context.Background()` dan **tidak** dibatalkan saat klien memutus koneksi, jadi query lambat tidak otomatis dihentikan.
- **Setiap `ORDER BY` yang berpasangan dengan `LIMIT`/`OFFSET` diakhiri kolom unik** (`ORDER BY nama, id`). Mengurutkan hanya pada kolom tak unik membuat satu baris muncul di dua halaman sementara baris lain tidak pernah keluar.
- **Setiap string pencarian lewat `repository.EscapeLike`** sebelum jadi argumen query. Tanpa itu `%` dari pengguna mencocokkan segalanya, dan produk bernama `100%` tidak akan pernah bisa ditemukan. Ini bug kebenaran, bukan injeksi — `$1` aman keduanya.
- **Tulis filter sekali** sebagai konstanta paket dan pakai untuk `COUNT` maupun query baris; dua salinan akan melenceng dan `total_item` tidak lagi cocok dengan datanya.
- **Jangan pernah `SELECT *`.** Daftar kolom jadi konstanta supaya urutan `Scan` tidak bisa melenceng saat migrasi menambah kolom.
- Kolom nullable di-scan ke pointer atau `sql.NullXxx`.
- Indeks unik tidak mengikat `NULL`, jadi berapa pun baris boleh berbagi `kode = NULL`. `ExistsByKode` hanya dipanggil kalau kode benar-benar dikirim.
- Keunikan kode master **tidak peka huruf**, lewat `CREATE UNIQUE INDEX ... (lower(kode))` di migrasi `000009`. Pemeriksaan keberadaan memakai `lower(...) = lower($1)`.
- Check-then-insert tidak menjamin keunikan — dua request bisa lolos berdua. `repository.UniqueViolation` memetakan SQLSTATE `23505` supaya yang kalah balapan dapat 409, bukan 500. Pre-check tetap ada hanya demi pesan yang lebih ramah.

## Produk, satuan konversi, dan harga jual

Tiga tabel yang saling terikat: `product`, `product_satuan`, dan `product_harga_jual`.

**Satuan dasar didaftarkan otomatis dengan `faktor = 1`.** Tidak ada constraint database untuk itu, jadi kalau bergantung pada pengirim request, satu produk yang lolos tanpa satuan dasar akan merusak setiap konversi yang dibangun di atasnya. `POST /api/v1/product` menyisipkannya sendiri dari `id_satuan_dasar`, di transaksi yang sama dengan produknya.

`faktor` bertipe `BIGINT`, jadi konversinya **wajib bilangan bulat** — satuan yang memuat 2,5 satuan dasar tidak bisa diwakili.

**Harga jual berversi, dan versinya tidak boleh tumpang tindih.** `POST /api/v1/product/{id}/harga-jual` menutup versi yang masih terbuka pada `berlaku_dari` lalu membuka yang baru, dalam satu transaksi. Rentangnya setengah terbuka `[)`, jadi menutup pada tanggal itu tidak meninggalkan celah maupun tumpang tindih:

| Versi | `berlaku_dari` | `berlaku_sampai` | Berlaku |
|---|---|---|---|
| lama | 2026-01-01 | 2026-03-01 | 1 Jan – 28 Feb |
| baru | 2026-03-01 | `null` | 1 Mar – seterusnya |

Tumpang tindih menjawab **409**, ditegakkan exclusion constraint GiST `product_harga_jual_no_overlap`. Itu satu-satunya penjaga yang nyata — pemeriksaannya melintasi baris, jadi pengecekan di Go tidak bisa menggantikannya: dua request bersamaan bisa sama-sama tidak menemukan tumpang tindih lalu sama-sama menulis.

Beberapa hal lain yang tidak terlihat dari daftar endpoint:

- **`kode_barang` dan `id_satuan_dasar` tidak bisa diubah** dan tidak ada di DTO update. `kode_barang` mengidentifikasi barang di setiap dokumen yang merujuknya; menggantinya menulis ulang makna dokumen lama. Mengubah `id_satuan_dasar` lebih parah — membatalkan seluruh `faktor` di `product_satuan` dan setiap kuantitas yang sudah diposting ke `kartu_stok` dalam satuan dasar lama. Pensiunkan dengan `is_aktif: false` lalu buat produk baru.
- **Maksimal satu `is_default_input` per produk**, ditegakkan partial unique index dari migrasi `000011`. `POST .../satuan` dengan `is_default_input: true` **memindahkan** penandanya: yang lama dibersihkan lebih dulu di transaksi yang sama.
- Harga hanya boleh dibuat untuk satuan yang **sudah terdaftar** di `product_satuan`. Tidak ada foreign key yang menjaga itu, jadi ditolak 400 di usecase — harga untuk satuan yang tidak dijual tidak akan pernah muncul di layar input.
- `product_harga_jual` hanya **sumber pengisian harga otomatis**. Harga yang benar-benar ditagih disalin sebagai snapshot ke `penjualan_detail`, jadi mengubah harga master tidak pernah menyentuh transaksi lama. Bedanya keduanya berarti ada override manual yang bisa dilaporkan.
- Endpoint list **tidak** membawa `satuan` maupun `harga_jual`; mengambilnya berarti satu query per baris. Kuncinya hilang sama sekali, bukan array kosong, supaya bedanya dengan "produk ini memang tidak punya satuan" tetap jelas.
- Tidak ada kolom stok di sini. `kartu_stok` satu-satunya sumber kebenaran stok, dan modul ini tidak menyentuhnya.
- **Ini modul pertama yang benar-benar mengisi `created_by`/`updated_by`**, diambil dari token lewat `middleware.SessionFrom` — `product.created_by` `NOT NULL`, dan itulah yang membuat modul ini harus menunggu autentikasi.

## Pembelian: faktur, penerimaan, dan posting stok

Dokumen transaksi pertama, dan yang pertama menyentuh `kartu_stok`. Yang didigitalkan bukan fakturnya — faktur kertas tetap datang dari luar — melainkan **pencocokan antara faktur dan isi box**, beserta akibatnya.

**Tidak ada purchase order.** Pemesanan terjadi lewat WhatsApp; menginput ulang PO setelah kesepakatan sudah jadi di chat adalah input ganda tanpa imbalan.

### Dua kolom kuantitas

> **Stok ikut barang, utang ikut faktur.**

| Kolom | Isi | Dipakai untuk |
|---|---|---|
| `qty_faktur` / `qty_dasar` | yang tertulis di kertas | subtotal, PPN, utang ke supplier |
| `qty_diterima` / `qty_diterima_dasar` | hasil hitung fisik dari box | `kartu_stok`, alokasi ongkir |

`qty_diterima` boleh dikosongkan dan artinya sama dengan `qty_faktur` — kasus biasa di mana semuanya memang ada. Selisih **tidak memblokir posting** (konfirmasi ke supplier terjadi di WhatsApp, di luar aplikasi) tapi **wajib diberi keterangan**, karena catatan itulah yang dibaca kembali saat menghubungi supplier. Kirim lebih dari faktur **ditolak**: barang yang tidak difakturkan tidak punya nilai.

### Jebakan nilai yang akibatnya permanen

Faktur 100 pcs @ 10.000 dengan alokasi ongkir 50.000 → nilai baris 1.050.000. Yang datang 95.

| | `nilai_masuk` | HPP jadi | |
|---|---|---|---|
| ❌ kirim nilai faktur penuh | 1.050.000 untuk 95 pcs | **11.052/pcs** | terlalu tinggi |
| ✅ proporsional ke qty diterima | 95/100 × 1.050.000 = 997.500 | **10.500/pcs** | benar |

Ini tidak benar sendiri saat susulan datang. `kartu_stok` memakai rata-rata bergerak dan baris keluar mengunci harga pokok yang berlaku saat itu, jadi penjualan yang terjadi di sela dua kiriman membukukan HPP yang melambung itu **permanen** ke harga pokok penjualan — dan tabelnya append-only, tidak bisa diperbaiki, hanya dibalik.

### Alur persetujuan

```
DRAFT ──POST /ajukan (INVENTARIS)──▶ DIAJUKAN
  ▲                                    │
  │                                    ├─ POST /posting (SUPERADMIN) ──▶ POSTED
  └── POST /tolak (SUPERADMIN) ────────┘                                   │
      + alasan                                                             │
                                           POST /batal (SUPERADMIN) ◀──────┘
                                           tulis baris kartu_stok pembalik
```

Header dan detail hanya bisa diubah saat `DRAFT`. Setiap transisi mengambil row lock lebih dulu (`SELECT ... FOR UPDATE`), karena tanpa itu dua request posting bersamaan sama-sama membaca `DIAJUKAN`, sama-sama lolos, dan stoknya naik dua kali.

### Penomoran dokumen

Format `BL/2026/08/0001`, mereset tiap bulan mengikuti **tanggal dokumen** — faktur Juli yang baru diketik bulan Agustus tetap dapat nomor Juli. Counter-nya di tabel `document_counter` dan diambil dengan satu `INSERT ... ON CONFLICT DO UPDATE RETURNING` di dalam transaksi dokumen: statement itu atomik dan mengunci barisnya sampai commit, jadi dua request bersamaan antre alih-alih menghasilkan nomor kembar. Nomor bisa berlubang kalau transaksinya rollback — jauh lebih murah daripada dua dokumen bernomor sama.

Generatornya dibuat lintas modul sejak awal (dikunci per `prefix`), karena penjualan, mutasi, pemakaian, dan dokumen pembayaran butuh hal yang sama — `MT`, `PM`, dan `PU` sudah memakainya.

### Ongkir

`biaya_angkut = total_koli × tarif_per_koli`, dan **bukan bagian dari `total`** — itu tagihan ekspedisi, bukan utang ke supplier. Ia masuk pembukuan lewat `alokasi_biaya` di tiap baris, sebanding `jumlah_koli`, terhadap **`qty_diterima`** bukan `qty_faktur` (ongkir dibayar untuk barang yang benar-benar diangkut).

- Hasil alokasinya berjumlah **persis** `biaya_angkut`; sisa pembulatan didorong ke baris dengan basis terbesar
- Posting menolak dokumen yang jumlah `jumlah_koli`-nya tidak sama dengan `total_koli`. `POST /pembelian/{id}/bagi-rata-koli` membagikannya sebanding `qty_dasar` supaya tidak perlu dihitung tangan
- Semua `jumlah_koli` nol → jatuh ke metode `QTY`, bukan membagi dengan nol
- `ditanggung_supplier: true` berarti ongkir sudah masuk nota supplier dan **tidak dialokasikan lagi**

### Hal lain yang tidak terlihat dari daftar endpoint

- **`no_faktur_supplier` unik per supplier**, tidak peka huruf besar-kecil. Tanpa PO, dokumen ini satu-satunya jejak faktur supplier — nota yang sama diinput dua kali akan menaikkan stok dua kali. Dokumen `BATAL` melepas nomornya kembali supaya nota yang salah input bisa diinput ulang.
- **Seluruh uang dan kuantitas berupa string desimal**, di request maupun response. Aritmetikanya memakai `math/big.Rat` — eksak sampai satu pembulatan yang disengaja di akhir. Tidak ada float yang menyentuh uang.
- `faktor_konversi` disalin sebagai **snapshot** dari `product_satuan` saat baris ditulis. Master boleh berubah; dokumen lama tidak boleh ikut berganti arti. `qty × faktor` wajib bilangan bulat karena `qty_dasar` bertipe `BIGINT`.
- Pembatalan menulis baris pembalik dengan `id_kartu_stok_asal` terisi. **Nilainya mengikuti rata-rata bergerak yang berlaku sekarang, bukan harga pokok baris aslinya** — trigger `kartu_stok_hitung_saldo` menimpa `nilai_keluar` dan `harga_pokok_satuan` setiap baris keluar, jadi harga pokok yang dikirim aplikasi diabaikan. Itu sifat metode rata-rata bergerak.
- `GET /pembelian/{id}/sisa` hanya membawa baris yang belum lengkap — daftar kerja untuk mengejar susulan lewat WhatsApp, dan input untuk dokumen `penerimaan_susulan` di bawah.
- Setiap baris pada `GET /pembelian/{id}` membawa `qty_dapat_diretur` — input untuk dokumen `retur_pembelian`. Tidak ada endpoint tersendiri untuknya: angkanya sudah ada di layar tempat retur diketik.

## Penerimaan susulan: barang yang datang belakangan

Premisnya bukan penerimaan parsial yang umum di ERP. Di sini supplier kehabisan stok lalu mengirim yang ada dulu — tidak direncanakan — dan **fakturnya tetap satu**, terbit penuh di kiriman pertama.

| | Umum di ERP | Kasus kita |
|---|---|---|
| Pemicu kiriman terpisah | pesanan sengaja dipecah jadwal | supplier kehabisan stok |
| Faktur | satu per kiriman | **satu untuk seluruhnya** |
| Utang | bertambah tiap kiriman | **dibukukan sekali penuh** di awal |
| Yang dilacak | qty dipesan vs diterima per jadwal | **sisa per baris saja** |

Jadi dokumen ini **menambah stok dan tidak pernah menambah utang**.

### Kenapa dokumen baru, bukan mengubah `qty_diterima`

`pembelian` yang sudah POSTED tidak boleh diubah, dan `kartu_stok` bersifat append-only. Menaikkan `qty_diterima` pada baris yang sudah diposting akan melanggar imutabilitas dokumen, membuat pembatalan mustahil diaudit (baris `kartu_stok` mana yang harus dibalik?), dan menghapus jejak kapan susulan benar-benar datang.

Bentuk yang cocok adalah cermin dari `retur_pembelian` — sama-sama menunjuk `pembelian_detail`, arah barangnya berlawanan:

```
pembelian (faktur, utang, penerimaan pertama)
    ├── retur_pembelian      → barang keluar, menunjuk pembelian_detail
    └── penerimaan_susulan   → barang masuk,  menunjuk pembelian_detail
```

### Satu faktur menyumbang persis nilainya sendiri

Harga pokok **disalin** dari `pembelian_detail.harga_pokok_satuan_dasar`, tidak pernah dihitung ulang dan tidak pernah dibaca dari rata-rata terkini. Harga barang sudah ditetapkan faktur; kiriman keduanya tidak mengubahnya.

```
Faktur 100 pcs @10.000 + ongkir 50.000 = 1.050.000, datang 95

kiriman 1 (pembelian)         95 x 10.500 =   997.500
kiriman 2 (penerimaan susulan) 5 x 10.500 =    52.500
                                            ─────────
nilai persediaan dari faktur ini            1.050.000  ← persis
```

Rata-rata bergeraknya juga tidak bergeser: setiap unit dari faktur itu berharga sama.

### Sisa, dan tiga angka yang berbeda

```
selisih_dasar     = qty_dasar − qty_diterima_dasar    kurang di kiriman pertama, beku
qty_susulan_dasar = Σ susulan POSTED                  yang sudah menyusul
sisa_dasar        = selisih_dasar − qty_susulan_dasar yang masih ditagih hari ini
```

`pembelian.status_penerimaan` adalah cache, dan dokumen inilah yang mengubah jawabannya setelah pembelian sudah POSTED dan tidak bisa lagi menghitungnya sendiri.

### Hal lain yang perlu diketahui

- **Pembelian asalnya harus `POSTED`.** Sebelum itu barisnya belum punya harga pokok untuk disalin dan belum punya sisa yang pasti.
- **Pemeriksaan sisa yang menentukan terjadi saat posting**, di bawah row lock pembelian. Yang saat create hanya memberi error lebih cepat: di antara draft ditulis dan diposting, susulan lain untuk pembelian yang sama bisa lebih dulu menghabiskan sisanya. Dua draft boleh sama-sama mengklaim sisa yang sama — draft bukan pengiriman.
- **Satuan boleh berbeda dari fakturnya.** Lima pcs kurang dari baris yang diketik dalam dus adalah kasus wajar, jadi `faktor_konversi` diambil segar dari `product_satuan` (kuantitasnya hitungan baru) sementara harga pokoknya disalin dari sumber (harganya sudah ditetapkan faktur).
- **`id_supplier` dan `id_ruang` disalin dari pembelian**, tidak dipilih. Barang yang perlu pindah ruang setelah diterima adalah pekerjaan mutasi.
- Baris `kartu_stok`-nya memakai `jenis_transaksi = 'PENERIMAAN_SUSULAN'`, jadi laporan mutasi stok bisa memisahkan barang yang datang tepat waktu dari yang menyusul tanpa join ke tabel dokumen.
- **Membatalkan pembelian yang punya susulan POSTED ditolak 409** — batalkan susulannya lebih dulu. Pembatalan pembelian hanya membalik baris yang ditulis pembelian itu sendiri, jadi stok susulannya akan tertinggal tanpa dokumen yang menjelaskannya, dan setelah itu tidak bisa dibalik lagi karena jalur pembatalannya menuntut pembelian yang masih POSTED.
- Nomornya seri sendiri, `PS/2026/08/0001`, dari generator yang sama.

## Retur pembelian: barang yang dikirim balik

Cermin dari penerimaan susulan, dan dibangun sebagai cermin dengan sengaja. Dua-duanya menunjuk baris `pembelian_detail` dari pembelian yang sudah POSTED, dua-duanya **menyalin** harga pokoknya dari sana alih-alih menghitung sendiri, dan dua-duanya mengambil kuota dari baris itu. Yang berbeda hanya arah barangnya — dan satu perbedaan itulah yang membuat pembatalannya jadi bagian yang halus.

```
pembelian (faktur, utang, penerimaan pertama)
    ├── retur_pembelian      → barang keluar, menunjuk pembelian_detail
    └── penerimaan_susulan   → barang masuk,  menunjuk pembelian_detail
```

### Yang bisa diretur adalah yang benar-benar datang

```
qty_retur_dasar   = Σ retur POSTED
qty_dapat_diretur = qty_diterima_dasar + qty_susulan_dasar − qty_retur_dasar
```

Perhatikan kuantitas yang **tidak** ikut: `qty_dasar`. Yang difakturkan adalah yang ditagih supplier, dan barang yang tidak pernah datang tidak bisa dikirim balik — kekurangan kiriman dikejar dengan penerimaan susulan, bukan retur.

Ini sumbu yang berbeda dari `sisa_dasar`, dan mencampurnya adalah kesalahan yang perlu dihindari: **barang yang diretur tetap pernah diterima**, jadi retur tidak membuat supplier berutang barang lagi dan tidak memberi hak atas kiriman susulan. `pembelian.status_penerimaan` karena itu sengaja tidak dihitung ulang saat retur diposting. Satu baris bisa punya `sisa_dasar` dan `qty_dapat_diretur` yang sama-sama bukan nol.

### Pembelian dan returnya saling menghapus

Harga pokok disalin dari `pembelian_detail.harga_pokok_satuan_dasar`, bukan dibaca dari rata-rata terkini. Itulah yang menurut migrasi `000005` menjadi alasan kolom itu ada di tabel ini.

```
Faktur 100 pcs @10.000 = 1.000.000, semuanya datang

pembelian         100 x 10.000 = 1.000.000 masuk
retur_pembelian   100 x 10.000 = 1.000.000 nilai returnya
                                 ─────────
nilai persediaan                         0  ← bersih
```

> [!IMPORTANT]
> **`total` dokumen ini dan nilai yang dicatat kartu stok tidak selalu sama, dan itu bukan bug.** `total` adalah nilai barang menurut faktur. Sementara `kartu_stok` menilai setiap baris keluar pada rata-rata bergerak yang berlaku saat itu, karena barangnya sudah tercampur dengan stok lama sejak ia datang dan tidak ada lagi batch yang bisa dipisahkan. Dua-duanya jawaban benar untuk pertanyaan yang berbeda.
>
> **Karena itu `total` juga bukan angka yang dikreditkan supplier.** Ia sudah termasuk porsi ongkir dan perlakuan PPN, sementara `pembelian.total` tidak memasukkan ongkir sama sekali; mengurangkan yang satu dari yang lain akan melebihkan kredit sebesar uang yang dibayar ke ekspedisi, bukan ke supplier. Utang punya angkanya sendiri, `nilai_kredit_utang`, yang dibekukan saat posting — lihat [Pembayaran utang](#pembayaran-utang-uang-yang-keluar-ke-supplier).

### Hal lain yang perlu diketahui

- **Pembelian asalnya harus `POSTED`.** Sebelum itu barisnya belum punya harga pokok untuk disalin, dan belum ada barang yang datang untuk dikirim balik.
- **Pemeriksaan kuota yang menentukan terjadi saat posting**, di bawah row lock pembelian — sama seperti penerimaan susulan. Dua draft boleh sama-sama mengklaim barang yang sama; yang kedua gagal saat mencoba mengambil apa yang sudah diambil pertama.
- **`alasan` wajib** meski kolomnya nullable, dan patch tidak boleh mengosongkannya. Ia satu-satunya catatan kenapa barang yang sudah dibayar dikirim balik, dan itu yang dibacakan ke supplier.
- **Satuan boleh berbeda dari fakturnya.** Satu dus dikembalikan dari baris yang diketik dalam pcs adalah kasus wajar: `faktor_konversi` diambil segar dari `product_satuan` (kuantitasnya hitungan baru atas barang yang sedang dikemas), harga pokoknya disalin dari sumber.
- **`id_supplier` dan `id_ruang` disalin dari pembelian**, tidak dipilih. Barang yang perlu dikembalikan dari ruang lain adalah pekerjaan mutasi lebih dulu.
- Baris `kartu_stok`-nya memakai `jenis_transaksi = 'RETUR_PEMBELIAN'` dan `stok_keluar`. Nilai enumnya sudah ada di migrasi `000002`, jadi modul ini tidak perlu `ALTER TYPE` — kebetulan yang menyenangkan, karena `ADD VALUE` tidak bisa dibatalkan.
- **Retur POSTED mengurangi utang, dan pembatalannya mengembalikannya.** `pembelian.status_pembayaran` dihitung ulang setelah posting maupun setelah batal. `nilai_kredit_utang` tetap tertinggal di baris dokumen `BATAL` sebagai catatan berapa yang pernah diklaim, tapi penghitungan ulangnya hanya menjumlahkan retur POSTED, jadi uangnya kembali jadi utang.
- **Membatalkan pembelian yang punya retur POSTED ditolak 409** — batalkan returnya lebih dulu. Pembatalan pembelian membalik seluruh kuantitas yang diterima, sementara returnya sudah mengeluarkan sebagiannya, jadi pembalikannya akan menekan saldo di bawah nol; dan kalaupun saldonya cukup, returnya akan tertinggal menunjuk pembelian `BATAL` yang pembalikannya sudah memperhitungkan barang yang sama.
- **Membatalkan penerimaan susulan yang barangnya sudah diretur ditolak trigger** dengan 400, bukan oleh pengecekan di Go. Itu arbiter yang tepat: saldonya dihitung di dalam trigger di bawah advisory lock, justru supaya tidak ada pembaca yang bisa memutuskannya lebih dulu.
- Nomornya seri sendiri, `RB/2026/08/0001`, dari generator yang sama.

## Riwayat harga beli: pengganti purchase order

`GET /api/v1/product/{id}/riwayat-beli`

Sistem ini sengaja **tidak punya purchase order** — pemesanan terjadi lewat WhatsApp, dan mengetik ulang PO setelah kesepakatan sudah tercapai di chat adalah input ganda tanpa imbalan. Yang sebenarnya dibutuhkan sebelum memesan bukan dokumen pesanan, melainkan kebiasaan "cek harga ke beberapa toko dulu":

> produk X terakhir dibeli dari supplier mana, harga berapa, tanggal berapa

Dan itu sudah terkumpul sendiri. Setiap `pembelian` yang diposting mencatat harga yang **benar-benar dibayar** — lebih berguna daripada penawaran, karena penawaran hanya yang dijanjikan di chat. Tidak ada yang perlu diinput dan tidak ada yang bisa jadi tidak sinkron: ini query, bukan modul.

**Satu baris per supplier**, yaitu pembelian terakhir supplier itu atas produk tersebut — bukan satu baris per dokumen. Diurutkan dari yang terbaru. `id_supplier` mempersempit ke satu supplier, dan itu bentuk yang dipakai layar input pembelian: headernya sudah menyebut supplier, jadi pertanyaan yang berguna di sana adalah "terakhir saya bayar berapa ke supplier ini".

**Hanya dokumen `POSTED`.** Draft cuma kertas yang sudah diketik, dan dokumen `BATAL` adalah pembelian yang ditarik kembali; keduanya bukan harga yang pernah dibayar siapa pun.

### Dua harga, dua pertanyaan yang berbeda

Ini yang paling mudah disederhanakan jadi satu angka, dan tidak boleh:

| Kolom | Isi | Dipakai untuk |
|---|---|---|
| `harga_satuan_dasar` | `subtotal / qty_dasar` — faktur setelah diskon baris | membandingkan dengan penawaran supplier berikutnya |
| `harga_pokok_satuan_dasar` | setelah diskon nota, bagian PPN, dan ongkir | menilai margin |

10 DUS isi 12 seharga 120.000/DUS adalah 1.200.000 untuk 120 pcs — **10.000/pcs di kertas**. Ditambah tagihan ekspedisi 60.000, barangnya jadi **10.500/pcs di rak**. Menawar pakai angka kedua berarti menuntut supplier bertanggung jawab atas ongkos yang bukan tagihannya; menilai margin pakai angka pertama berarti melewatkan ongkir sama sekali.

`harga_satuan_input` juga dilaporkan karena itu angka yang tercetak di nota, tapi **bukan angka pembanding**: satuannya mengikuti apa yang diketik, jadi 120.000/DUS dan 10.000/PCS terlihat jauh berbeda padahal harga yang sama.

### Hal lain yang perlu diketahui

- **Produk yang tidak dikenal menjawab 404; produk yang belum pernah dibeli menjawab halaman kosong.** Dua fakta yang berbeda, dan klien yang tidak bisa membedakannya akan menampilkan pesan yang salah.
- **Tidak ada migrasi baru.** Fase ini tidak menambah satu kolom pun — seluruh jawabannya sudah ada di `pembelian` dan `pembelian_detail` yang diposting fase 2.
- Kalau satu dokumen memuat produk yang sama di dua baris, yang diambil adalah baris yang diketik terakhir. Ditegakkan pemecah seri di `ORDER BY`, bukan diserahkan ke planner.

## Pembayaran utang: uang yang keluar ke supplier

Fase terakhir isu #4, dan **satu-satunya modul transaksi yang tidak menyentuh stok sama sekali**. Itu bukan detail sepele — ia yang menentukan hampir seluruh bentuknya.

```
pembayaran_utang (uang keluar)
    └── pembayaran_utang_alokasi → menunjuk pembelian, banyak-ke-banyak
```

Satu pembayaran boleh menutup beberapa faktur, dan satu faktur boleh ditutup beberapa pembayaran. Karena itulah alokasi jadi tabel sendiri, bukan kolom di salah satu ujungnya.

### Tidak ada `DIAJUKAN`, dan itu disengaja

`DRAFT → POSTED → BATAL`. Tahap persetujuan yang ada di `pembelian` sengaja tidak ditiru, karena alasan keberadaannya tidak berlaku di sini: `kartu_stok` append-only, jadi posting stok yang salah hanya bisa dibalik dan pembalikannya dinilai pada rata-rata yang sudah bergeser. Alokasi pembayaran tidak begitu — ia bisa dibatalkan dan seluruh cache dihitung ulang persis, tanpa residu penilaian apa pun. Menambahkan `DIAJUKAN` berarti meniru mekanismenya tanpa alasannya.

Kontrol dua orangnya tetap ada, hanya satu state lebih sedikit: `CASHIER` menyiapkan dokumen dan alokasinya, `SUPERADMIN` yang melepas uangnya.

### Tiga aturan yang tidak simetris

| | Batasnya |
|---|---|
| Pembayaran | dialokasikan **paling banyak** sebesar jumlahnya sendiri |
| Faktur | menerima **paling banyak** sisa utangnya, sudah memperhitungkan retur POSTED |
| Kelebihan bayar | **normal**, mengendap jadi kredit di supplier — jangan dipaksa pas |

Alokasi yang lebih kecil dari jumlah pembayaran bukan dokumen setengah jadi. Uang kadang dibayar dulu sebelum diputuskan faktur mana yang ditutupnya, dan `POST /api/v1/pembayaran-utang` menerima daftar alokasi yang kosong justru karena itu.

### Giro yang belum cair bukan pembayaran

> [!IMPORTANT]
> Menyerahkan giro tidak menyelesaikan apa pun. Utang berkurang saat girinya **cair**, bukan saat dokumennya diposting.

Posting giro `BELUM_CAIR` membekukan alokasinya dan menutup dokumennya, tapi meninggalkan utangnya tepat di tempatnya — dan itu memang yang seharusnya terjadi. `POST /{id}/cair` yang menggerakkan `status_pembayaran`, dan `POST /{id}/tolak-giro` memastikan giro yang ditolak tidak pernah menggerakkannya sama sekali.

Konsekuensinya batas sisa utang **diperiksa ulang saat pencairan**, bukan cukup saat posting: di antara keduanya faktur yang sama bisa sudah ditutup pembayaran tunai. `status_giro` wajib ada untuk metode `GIRO` dan wajib kosong untuk metode lain, ditegakkan CHECK di migrasi `000015` — giro tanpa status tidak bisa dibedakan dari giro yang sudah cair, dan itu justru perbedaan antara utang yang berkurang dan yang tidak.

### Berapa yang dikreditkan retur

Ini keputusan yang sengaja ditunda di fase 5 dan diselesaikan di sini. `retur_pembelian.total` **bukan** jawabannya: ia nilai persediaan menurut harga pokok, dan harga pokok memuat porsi ongkir yang dibayar ke ekspedisi. Utang butuh angkanya sendiri, dihitung dari nilai faktur lalu diskalakan:

```
nilai_faktur_retur = Σ (pembelian_detail.subtotal / qty_dasar) × qty_retur_dasar
nilai_kredit_utang = pembelian.total × nilai_faktur_retur / pembelian.subtotal
```

Penskalaan terhadap `total` — bukan mengambil mentah nilai baris fakturnya — karena `total` sudah memuat diskon nota, PPN, dan pembulatan. Mengembalikan seluruh barang lalu mengkredit seluruh nilai barisnya akan melebihkan kredit **persis sebesar diskon nota** yang justru mengurangi tagihannya. Bentuk ini membuat invariannya bisa diperiksa: kredit seluruh retur sebuah pembelian tidak pernah melebihi `total`-nya, dan pas sebesar `total` ketika semua barang kembali.

Dibekukan saat posting, tidak dihitung saat dibaca. Ia turunan dari baris dua dokumen yang sudah POSTED dan tidak bisa berubah, jadi menghitungnya ulang akan selalu memberi jawaban sama — sampai suatu hari tidak, dan saat itu utang lama diam-diam berganti nilai. Setiap angka uang di proyek ini snapshot.

### `status_pembayaran` adalah cache

Selalu dihitung ulang, tidak pernah di-set dari form — aturan yang sama dengan `status_penerimaan`.

```
sisa = pembelian.total − Σ alokasi efektif − Σ nilai_kredit_utang retur POSTED
```

"Efektif" berarti alokasi dari pembayaran POSTED yang bukan giro, atau giro yang sudah `CAIR`. Semua yang bisa mengubah jawabannya memanggil penghitungan ulang yang sama: posting dan batal pembayaran, cair dan tolak giro, serta posting dan batal retur. Satu statement SQL, jadi tidak ada jendela ketika cache-nya berbeda dari baris yang ia ringkas.

`SEBAGIAN` juga mencakup faktur yang baru dikurangi retur tanpa uang sepeser pun — memang sebagian terselesaikan, dan kedua angkanya dilaporkan berdampingan supaya layar bisa menyebut mana yang menyebabkannya.

### Hal lain yang perlu diketahui

- **`GET /api/v1/supplier/{id}/utang` adalah daftar kerjanya**, dan seperti riwayat harga beli ia query, bukan modul: tidak ada tabel, tidak ada migrasi. Defaultnya hanya faktur yang masih terbuka; `termasuk_lunas=true` membawa yang sudah selesai. Supplier tidak dikenal menjawab 404, supplier tanpa utang menjawab halaman kosong.
- **Pembelian tidak bisa dibatalkan saat sudah dibayar**, termasuk saat masih ada giro `BELUM_CAIR` yang menunjuknya. Giro yang belum cair belum mengurangi utang, tapi ia dokumen yang beredar di luar sana atas faktur itu.
- **Satu pembelian hanya boleh muncul sekali per pembayaran**, ditegakkan `pembayaran_utang_alokasi_baris_uidx`. Tanpa itu dua baris untuk faktur yang sama lolos pengecekan sisa sendiri-sendiri lalu bersama-sama melebihinya — jebakan yang sama seperti di penerimaan susulan dan retur.
- **Alokasi diganti wholesale** lewat `PUT /{id}/alokasi`, tidak diedit satu-satu — alasannya sama dengan baris pembelian.
- Nomornya seri sendiri, `PU/2026/08/0001`, dari generator yang sama.

## Lampiran berkas: unggah dulu, tempel kemudian

Faktur supplier datang sebagai kertas dan perlu difoto di meja penerimaan. Tapi kebutuhannya **lintas modul** — retur butuh foto barang rusak, penjualan butuh surat jalan bertanda tangan, stok opname butuh berita acara — jadi ini dibangun sekali sebagai infrastruktur, bukan sebagai kolom di salah satu dokumen. Referensinya polimorfik (`ref_table` + `ref_id`), mengikuti pola yang sudah dipakai `kartu_stok`.

Ini juga **job pertama untuk `cmd/worker`**, yang sampai sekarang hanya menunggu sinyal tanpa pekerjaan apa pun.

### Alurnya tidak bisa "unggah ke dokumen X"

Foto diambil **sebelum** dokumennya tersimpan: petugas memotret faktur sambil membongkar box, dan dokumen `pembelian`-nya belum tentu sudah dibuat. Jadi:

```
1. POST /api/v1/dokumen  (multipart)   → berkas tersimpan, dapat id
                                          ref_table & ref_id masih NULL  ← yatim
2. POST /api/v1/pembelian              → dokumen induknya baru ada di sini
3. POST /api/v1/dokumen/12/tempel      → { ref_table: "pembelian", ref_id: 77 }
   { formnya ditinggalkan begitu saja } → baris (1) tetap yatim selamanya
                                          └── ini yang disapu worker
```

**`ref_id` yang nullable itu justru intinya.** Ia yang membuat berkas yatim mungkin ada, dan sekaligus membuatnya gampang dicari: satu indeks parsial `WHERE ref_id IS NULL` yang tetap kecil berapa pun besarnya tabel, karena isinya hanya yang belum tertempel.

Penempelan lewat satu endpoint di modul ini, bukan lewat field `dokumen_ids` di setiap dokumen. Konsekuensinya: modul yang mulai menerima lampiran cukup ditambahkan satu baris ke whitelist `repository.RefTableDokumen` — tanpa migrasi, tanpa mengubah DTO-nya sendiri, dan tanpa mengulang aturan yang sama di tempat kedua.

### Berkas dari luar adalah permukaan serangan

Hal-hal berikut bukan preferensi:

- **Nama berkas dari klien tidak pernah menyentuh filesystem.** Nama simpan dihasilkan server (UUID + ekstensi), `nama_asli` hanya untuk ditampilkan. Tanpa pemisahan ini, `../../config.json` adalah path yang sah. Lapisan penyimpanan **menolak**, bukan membersihkan, nama yang bukan nama berkas polos — `filepath.Base` akan diam-diam mengubah `../../etc/passwd` jadi `passwd` lalu melanjutkan, dan bug yang tidak bersuara lebih buruk daripada galat.
- **MIME ditentukan dari isi berkas** (magic bytes), bukan dari header `Content-Type` yang sepenuhnya dikendalikan pemanggil. HTML bernama `faktur.pdf` ditolak 400, dan ekstensi simpannya diturunkan dari hasil deteksi itu — bukan dari nama aslinya.
- **Batas ukuran ditegakkan saat mengalir** (`io.LimitReader`), bukan setelah berkas utuh masuk memori. Ukuran yang diklaim header multipart hanya dipakai untuk menolak lebih awal; yang mengikat adalah hitungan byte yang benar-benar ditulis.
- **Unduhan tetap di balik autentikasi**, dan selalu disajikan `Content-Disposition: attachment` dengan `X-Content-Type-Options: nosniff`, sehingga HTML atau SVG yang entah bagaimana tersimpan tidak bisa dieksekusi di origin aplikasi. Foto faktur memuat harga beli dan identitas supplier.
- **Urutan tulis berkas dulu, baru baris**, dan kalau barisnya gagal berkasnya dihapus lagi. Urutan sebaliknya meninggalkan baris yang menunjuk berkas tidak ada — dan itu tidak bisa diperbaiki siapa pun, karena tidak ada yang tahu isinya seharusnya apa.
- **Cronjob menghapus berdasarkan baris tabel, tidak pernah dengan memindai direktori.** Memindai direktori berarti ikut menghapus berkas yang barisnya belum sempat ter-commit — yaitu detik-detik antara berkas ditulis dan transaksinya selesai.

### Pembersihan berkas yatim

Job harian di worker, `time.Ticker` dan bukan pustaka cron: satu job harian butuh jeda antar-jalan, bukan spesifikasi jadwal. Naikkan ke `robfig/cron` saat ada job kedua yang jadwalnya benar-benar berbeda.

- Umur sebelum dianggap yatim: `dokumen.orphan_ttl_hours`, default **24 jam** — cukup longgar untuk form yang ditinggal semalam.
- Jeda antar sapuan: `dokumen.cleanup_interval`, default **24 jam**. Sekali jalan langsung saat proses naik, karena worker yang di-restart harian tidak akan pernah sampai ke tick pertamanya.
- **Berkasnya dihapus, barisnya bertahan** dengan `deleted_at` terisi. Jejak bahwa pernah ada unggahan lebih berharga daripada satu baris yang dihemat — dan itu pula yang membuat job ini bisa dijalankan ulang tanpa perlu mengingat apa yang sudah dikerjakannya.
- Aman terhadap worker ganda **dua lapis**: `pg_advisory_lock` menahan worker kedua di luar (pola yang sudah dipakai trigger `kartu_stok`), dan tiap baris dikerjakan dalam transaksinya sendiri di bawah row lock — jadi berkas yang sedang ditempelkan tepat saat disapu akan memblokir, bukan kehilangan isinya.

### Hal lain yang perlu diketahui

- **Yang diterima hanya JPEG, PNG, dan PDF**, maksimal `dokumen.max_size_mb` (default 10 MB — foto dari HP sekarang menembus 5 MB tanpa usaha). Batas body Fiber diturunkan dari kunci itu plus satu MB untuk overhead multipart; default Fiber cuma 4 MB dan akan diam-diam memangkas batas yang dikonfigurasi.
- **Maksimal 10 lampiran per dokumen.** Cukup untuk faktur berhalaman banyak yang difoto satu per satu; lebih dari itu tandanya retry yang macet.
- **Penghapusan hanya selama masih yatim atau induknya `DRAFT`.** Setelah dokumennya diajukan, foto faktur yang jadi dasar persetujuan adalah bagian dari rekaman.
- **Menempel ke dokumen `BATAL` ditolak**, karena lampiran di sana tidak akan pernah bisa dilepas lagi — aturan penghapusan hanya melepaskan induk yang `DRAFT`.
- `checksum_sha256` dipakai mendeteksi faktur yang sama diunggah dua kali. Hasilnya dilaporkan sebagai `duplikat_dari_id`, **bukan penolakan**: satu hasil scan sah saja ditempel ke dua dokumen berbeda.
- Penyimpanannya di balik satu interface (`repository.DokumenStorage`), jadi menggantinya dengan S3/MinIO tidak menyentuh usecase. Yang berjalan sekarang disk lokal, dan konsekuensinya **`web` tidak bisa discale lebih dari satu instance** — dua instance dengan volume sendiri-sendiri masing-masing memegang separuh lampiran.

## Tutup buku: periode yang menolak posting

Tabel `periode` sudah ada sejak migrasi `000002` dan trigger `kartu_stok` sudah menghormatinya sejak `000004` — tapi sampai isu #6 tidak ada satu baris Go pun yang menyentuhnya, jadi tidak ada tutup buku: setiap bulan terbuka selamanya.

Modul ini yang paling mendekati master data, bukan dokumen transaksi: tidak ada nomor, tidak ada baris detail, tidak ada posting. Yang membuatnya berbeda dari `supplier` cuma dua hal — endpoint aksinya (`POST /{...}/tutup`) dan row lock yang diambil sebelum memutuskan.

**Menutup satu bulan menyentuh setiap modul yang menulis `kartu_stok`, sekarang maupun nanti.** `mutasi`, `pemakaian`, dan `penjualan` sudah mewarisinya tanpa satu baris kode saat modulnya masing-masing dibangun, dan stok opname akan begitu juga — penegakannya di trigger.

### Bulan tanpa baris dihitung terbuka

Ini keputusan `000004`, supaya database baru tidak macet sebelum ada data — dan konsekuensinya menentukan bentuk seluruh modul:

- Menutup sebuah bulan berarti **membuat** barisnya, bukan mengubah baris yang sudah ada. Karena itu `Tutup` adalah upsert.
- `GET /api/v1/periode/{tahun}/{bulan}` menjawab **`BUKA` sintetis** untuk bulan tanpa baris, bukan 404. "Tidak ada barisnya" dan "terbuka" adalah fakta yang sama; 404 malah mengundang klien menyimpulkan bulannya tidak ada.
- `GET /api/v1/periode` hanya memuat baris yang tersimpan, jadi bulan yang tidak pernah ditutup tidak muncul di sana betapapun terbukanya. Tabelnya mencatat penutupan, bukan kalender.
- Rutenya dikunci `(tahun, bulan)`, bukan `/{id}` seperti modul lain. Pasangan itulah identitas sebenarnya — `periode_tahun_bulan_uidx` sudah menyatakannya — dan rute ber-id justru tidak bisa menyebut kasus yang paling sering terjadi, karena bulan yang belum ditutup tidak punya id. Responsnya juga tidak memuat `id` sama sekali, sehingga bulan yang belum pernah ditutup punya bentuk yang sama persis dengan yang sudah.

### Pembalikan masuk periode berjalan, bukan periode dokumennya

Baris pembalik bertanggal **hari ini**, sedangkan posting bertanggal tanggal dokumen. Asimetri itu sudah ada di kode sejak `pembelian`; isu #6 yang memutuskannya secara eksplisit dan memakukannya dengan test.

Akibatnya **membatalkan dokumen yang periodenya sudah ditutup tetap berhasil**: pembaliknya jatuh di bulan berjalan yang masih terbuka, dan angka bulan tertutupnya tidak bergeser sedikit pun. Itu perlakuan akuntansi yang lazim — koreksi atas periode tertutup dibukukan di periode berjalan, bukan dengan membongkar yang sudah ditutup. Alternatifnya membuat dokumen salah ketik dari bulan lalu tidak punya jalan keluar sama sekali.

Yang perlu diketahui, karena tidak gratis: **status dokumen jadi tidak sinkron dengan buku bulan itu.** Dokumennya `BATAL`, sementara kartu stok bulan tertutup masih memuat pergerakannya, diimbangi baris pembalik di bulan lain. Laporan per periode harus membaca `kartu_stok`, bukan status dokumen.

Yang justru bisa menghalangi pembatalan adalah periode **berjalan** yang sedang `TUTUP` — tidak ada tempat membukukan koreksinya sampai ada yang membukanya kembali.

### Menutup dan memposting tidak bisa saling menyalip

Trigger membaca `periode` tanpa lock, dan di READ COMMITTED itu jendela nyata: transaksi posting membaca `BUKA`, penutupan commit, lalu postingnya commit — mendarat di bulan yang bukunya sudah tutup, tanpa ada yang melaporkannya. Bukan skenario khayalan kalau tutup buku dijalankan sore hari selagi orang masih menginput.

Migrasi `000017` menutupnya dengan **advisory lock**: trigger mengambil sisi *shared* atas `(tahun, bulan)` sebelum membaca statusnya, penutupan mengambil sisi *eksklusif*. Jadi penutupan menunggu setiap posting yang sedang berjalan untuk bulan itu, dan setiap posting yang mulai sesudahnya menunggu penutupan.

`SELECT ... FOR SHARE` atas barisnya tidak dipakai karena tidak bisa: bulan yang belum pernah ditutup **tidak punya baris**, dan menutup bulan berarti membuatnya — persis pada kasus yang paling sering terjadi, tidak ada apa pun untuk dikunci.

### Hal lain yang perlu diketahui

- **Boleh dibuka kembali, `SUPERADMIN` saja.** Bulan yang bisa dibuka siapa saja tidak pernah benar-benar tertutup, jadi tutup dan buka adalah satu kendali yang sama. `000017` menambah `dibuka_oleh` dan `ts_buka` supaya pembukaan meninggalkan jejak — tanpa itu, membuka lalu menutup lagi menimpa `ditutup_oleh`/`ts_tutup` tanpa sisa dan tidak ada yang tahu bulan itu pernah dibuka. Sepasang kolom, bukan tabel audit tersendiri: riwayat lengkap setiap penutupan adalah pertanyaan lain, dan tabelnya bisa ditambahkan kalau memang ditanyakan.
- **Menutup tidak harus berurutan.** Agustus boleh ditutup selagi Juli masih terbuka. Menuntut urutan berarti memaksa menutup bulan-bulan yang tidak pernah dipakai lebih dulu, dan tidak ada yang bisa rusak karena selanya — penegakannya per bulan di trigger, bukan saldo berjalan yang bulan terlewat merusaknya.
- **Menutup bulan yang sudah `TUTUP` menjawab 409**, begitu juga membuka bulan yang tidak sedang `TUTUP`. Keduanya tidak mengubah apa pun, dan 200 akan membuat pemanggil mengira ada yang berubah.
- **Tidak ada `DELETE`**, seperti seluruh proyek ini. Periode yang keliru ditutup dibuka kembali, tidak dihapus — menghapus barisnya juga menghapus satu-satunya catatan bahwa bulan itu pernah ditutup.
- **Pesan errornya sekarang menyebut satu sebab.** `RAISE` dari trigger tidak membawa nama constraint, jadi `invalidOnCheck` tidak bisa membedakan periode tertutup dari stok kurang, dan setiap pemanggil terpaksa bilang "periode sudah TUTUP **atau** saldo tidak mencukupi". Sekarang periode diperiksa lebih dulu di usecase sehingga pesannya berbunyi "periode 2026-07 sudah TUTUP". Yang menegakkan tetap trigger — pemeriksaan di Go hanya untuk pesan, persis seperti `ExistsByKode` terhadap unique index.
- Tabelnya tidak punya `created_at`/`updated_at` dan tidak ikut trigger `set_updated_at()`. Tidak ditambahkan: `ts_tutup` dan `ts_buka` sudah menjawab pertanyaan yang berguna, dan baris ini tidak berubah karena hal lain.

## Mutasi antar ruang: satu dokumen, dua baris kartu stok

Perpindahan barang dari satu `ruang` ke `ruang` lain — isu #7. Dokumen keempat yang menulis `kartu_stok`, dan yang **pertama menulis ke dua arah sekaligus**.

`MUTASI_MASUK` dan `MUTASI_KELUAR` bukan dua jenis dokumen. Keduanya adalah dua baris `kartu_stok` yang dihasilkan **satu** baris `mutasi_detail`, dalam satu transaksi — komentar migrasi `000007` sudah menyatakannya:

```
mutasi (id_ruang_asal → id_ruang_tujuan)
   └── mutasi_detail (produk, qty)
         ├── kartu_stok  MUTASI_KELUAR  di id_ruang_asal    (stok_keluar)
         └── kartu_stok  MUTASI_MASUK   di id_ruang_tujuan  (stok_masuk)
```

Memecahnya jadi dua dokumen berarti barang bisa keluar dari gudang tanpa pernah masuk ke toko, dan tidak ada yang menahannya. **Barang yang bertambah tanpa asal bukan mutasi** melainkan stok opname — `SO_SURPLUS`/`SO_DEFISIT` sudah disiapkan untuk itu.

Tabel `mutasi` dan `mutasi_detail` ada sejak migrasi `000007` dan nilai enumnya sejak `000002`, jadi **tidak ada `ALTER TYPE` sama sekali**. Migrasi `000018` hanya menambahkan `mutasi_status_check`, kolom pembatalan, dan indeks urutan baca.

### Nilai ikut ruang asal, dan hanya trigger yang tahu berapa

Ini jebakan termahal di modul ini, setara dengan nilai masuk proporsional di `pembelian`. Aturannya dari migrasi `000007`:

> Harga pokok wajib mengikuti ruang asal, kalau tidak memindahkan barang bisa mengubah total nilai persediaan.

Masalahnya, **aplikasi tidak bisa menghitung angka itu sendiri.** Rata-rata bergerak ruang asal hanya diketahui `kartu_stok_hitung_saldo()`, yang membacanya di dalam `pg_advisory_xact_lock` — apa pun yang dibaca Go sebelum insert sudah berpotensi basi. Trigger juga menimpa `nilai_keluar` pada setiap baris keluar, jadi mengirim angka sendiri percuma.

Yang menyelamatkan: `KartuStokRepository.Insert` mengembalikan seluruh barisnya lewat `RETURNING`, termasuk hasil hitungan trigger. Jadi urutan kedua insert itu terpaksa, dan keterpaksaannya justru yang benar:

```go
// 1. keluar dari ruang asal — trigger yang menilai
keluar := &entity.KartuStok{IDRuang: m.IDRuangAsal, StokKeluar: qtyDasar, /* ... */}
c.KartuStokRepository.Insert(ctx, tx, keluar)

// 2. masuk ke ruang tujuan, senilai persis apa yang keluar
masuk := &entity.KartuStok{IDRuang: m.IDRuangTujuan, StokMasuk: qtyDasar,
    NilaiMasuk: keluar.NilaiKeluar, /* ... */}
c.KartuStokRepository.Insert(ctx, tx, masuk)
```

| `nilai_masuk` baris tujuan | Akibat |
|---|---|
| ❌ dihitung ulang di Go dari HPP yang dibaca sebelum insert | bisa meleset kalau ada posting lain menyela — total nilai persediaan berubah hanya karena barang pindah ruang |
| ❌ diambil dari `harga_pokok_satuan_dasar` yang diketik di draft | HPP saat draft, bukan saat posting. Sama, dan diam-diam |
| ✅ `nilai_keluar` hasil `RETURNING` baris keluar | sama persis — Σ `nilai_akhir` seluruh ruang tidak bergerak |

Konsekuensinya **`mutasi_detail.harga_pokok_satuan_dasar` diisi saat posting, bukan saat draft** — salinan dari `harga_pokok_satuan` yang dilaporkan baris keluar. Kolomnya nullable memang untuk itu.

### Tanpa `DIAJUKAN`, dan alasannya berbeda dari `pembayaran_utang`

`DRAFT → POSTED → BATAL`. Satu-satunya penulis `kartu_stok` tanpa tahap persetujuan, jadi tidak ada endpoint `ajukan` maupun `tolak` — tujuh endpoint, bukan sembilan.

Aturan untuk tiga penulis `kartu_stok` sebelumnya berbunyi: pisahkan penjaganya per tahap alur, karena tabel itu append-only dan posting yang salah hanya bisa dibalik. Yang membedakan mutasi adalah **kesalahannya jauh lebih murah**:

| Dokumen | Yang terlanjur terjadi kalau salah |
|---|---|
| `pembelian` | utang ke pihak luar terbentuk, nilai persediaan bertambah, nomor faktur supplier terpakai |
| `retur_pembelian` | barang tercatat kembali ke supplier, utang berkurang, kredit membeku di `nilai_kredit_utang` |
| `mutasi` | barang tercatat di ruang yang keliru. **Total stok dan total nilai persediaan tidak bergerak sedikit pun** |

Tidak ada pihak luar, tidak ada uang, dan koreksinya adalah mutasi lagi ke arah sebaliknya — dokumen yang orang yang sama sudah berwenang membuatnya.

Alasannya **berbeda dari `pembayaran_utang`**, yang juga tidak punya `DIAJUKAN`. Di sana pembenarannya adalah alokasi tidak meninggalkan residu. Mutasi tidak seberuntung itu — pembatalannya tetap meninggalkan residu nilai. Pembenarannya bukan kerapian pembatalan, melainkan kecilnya taruhan.

Kendali dua orangnya tetap utuh dan pindah seluruhnya ke tabel rute: **`INVENTARIS` hanya sampai `DRAFT`, `SUPERADMIN` yang memposting dan membatalkan.** Yang hilang dari membuang `DIAJUKAN` cuma *state*-nya, bukan pemisahan wewenangnya.

Konsekuensi yang perlu disadari: **tidak ada lagi sinyal "draft ini sudah siap"**. `DRAFT` berarti dua hal sekaligus. Karena itu endpoint list wajib bisa memfilter `status=DRAFT`, dan `terlama_dulu=true` yang membuatnya terbaca sebagai antrean — paling lama dulu, seperti `GET /supplier/{id}/utang`, karena antrean dibaca untuk dikerjakan. Kalau nanti ternyata orang tetap butuh menandai "sudah siap", yang ditambahkan adalah `DIAJUKAN` — dan menambah nilai ke `mutasi_status_check` jauh lebih murah daripada membuangnya.

### Dua advisory lock dalam satu transaksi

Trigger mengambil `pg_advisory_xact_lock` per `(id_barang, id_ruang)`. Satu mutasi mengambil **dua** lock untuk produk yang sama, dan urutannya ditentukan arah barang:

```
dokumen A (gudang → toko)  : lock (X, gudang) lalu (X, toko)
dokumen B (toko → gudang)  : lock (X, toko)   lalu (X, gudang)
```

Klasik ABBA, dan sampai modul ini tidak pernah bisa terjadi karena tiap dokumen hanya menyentuh satu ruang. `KartuStokRepository.KunciSaldo` mengambil **seluruhnya di muka dalam urutan kanonik** sebelum insert pertama — mahalnya satu query, dan deadlock jadi mustahil, bukan sekadar jarang. Alternatifnya, memetakan SQLSTATE `40P01` ke 409 dan meminta klien mengulang, menyerahkan cacat nyata ke orang yang sedang berdiri di konter.

Lock periode diambil lebih dulu (`PeriodeRepository.LockShared`), karena itu urutan yang diambil trigger pada setiap insert. Penulis yang membaliknya membuka siklus lain: tutup buku antre di lock eksklusif, sebuah posting memegangnya shared sambil menunggu lock saldo kita, dan kita menunggu di belakang tutup buku itu.

`TestMutasiBerlawananArahTidakDeadlock` mematoknya, dan itu uji sungguhan: tanpa penguncian di muka, uji itu gagal dengan `deadlock detected (SQLSTATE 40P01)` di setiap kali dijalankan.

### Pembatalan tidak simetris nilainya

Pembatalan menulis baris pembalik dengan arah terbalik. Baris yang keluar dari ruang tujuan dinilai dengan **rata-rata bergerak ruang tujuan saat itu**, yang bisa sudah bergeser karena barang lain masuk ke sana. Jadi mutasi dan pembatalannya selalu berjumlah nol dalam kuantitas, tetapi **tidak selalu dalam nilai**.

Ini keterbatasan yang sama yang sudah tercatat untuk `pembelian`: trigger yang memiliki `harga_pokok_satuan` dan `nilai_keluar` setiap baris keluar, jadi menyalin nilai aslinya memang tidak tersedia. Membalikkannya butuh metode penilaian yang berbeda, bukan baris Go yang berbeda.

Selain itu **pembatalannya bisa ditolak 400** kalau barangnya sudah keluar lagi dari ruang tujuan: baris pembaliknya menyeret saldo tujuan ke negatif dan trigger menolak. Itu memang benar — barang yang sudah terjual dari toko tidak bisa dikembalikan ke gudang lewat pembatalan; obatnya mutasi lagi.

### Hal lain yang perlu diketahui

- **Produk yang sama boleh muncul di dua baris**, tidak seperti `penerimaan_susulan`, `retur_pembelian`, dan `pembayaran_utang` yang masing-masing punya unique index melarangnya. Di sana alasannya kuota per baris dokumen induk; di sini kuotanya saldo ruang asal, seluruh baris untuk satu produk dijumlahkan terhadapnya, dan trigger memeriksanya di setiap insert. 2 DUS dan 3 PCS barang yang sama adalah dua baris yang sah.
- **Kedua ruang boleh diubah selagi `DRAFT`**, berbeda dengan `pembayaran_utang` yang mengeluarkan `id_supplier` dari DTO update-nya: di sana alokasi menunjuk faktur supplier tertentu, di sini tidak ada baris detail yang menunjuk ruang.
- **Pemeriksaan saldo di Go hanya untuk pesannya**, persis seperti `periksaPeriode`. Trigger yang jadi penjaga sebenarnya, dan dia yang benar karena membaca di dalam lock. Yang dibeli pemeriksaan itu adalah kalimat yang menyebut produk dan kedua angkanya.
- Tidak ada satu pun kolom uang di headernya: tidak ada subtotal, diskon, PPN, ongkir, maupun koli. `math/big.Rat` tidak dibutuhkan sama sekali — satu-satunya angka desimal adalah harga pokok, dan itu datang dari database.
- Nomornya seri sendiri, `MT/2026/08/0001`, dari generator yang sama.
- Foto serah-terima barang bisa dilampirkan lewat `POST /api/v1/dokumen/{id}/tempel` dengan `ref_table = "mutasi"`. Biayanya satu baris di `repository.RefTableDokumen` dan tidak ada migrasi — memang itu desain isu #5.

## Pemakaian internal: permintaan, persetujuan berjenjang, dan posting

Barang yang dipakai sendiri — perbaikan, kantor, contoh — keluar dari gudang tanpa nota penjualan dan tanpa retur — isu #9. Dokumen kelima yang menulis `kartu_stok`, dan yang **pertama mengeluarkan barang tanpa lawan transaksi sama sekali**: tidak ada supplier, tidak ada pelanggan, tidak ada ruang tujuan. Barangnya habis, dan yang tersisa hanya catatan siapa yang meminta dan untuk apa.

Tabel `pemakaian`/`pemakaian_detail` ada sejak migrasi `000007` dan nilai enum `PEMAKAIAN` sejak `000002`, jadi **tidak ada `ALTER TYPE` sama sekali** — sama seperti `mutasi`. Migrasi `000021` hanya menyelaraskan kosakata status (`POSTED`/`BATAL`, menepati janji yang ditulis `000018`) dan menambah indeks urutan baca.

### Yang diposting adalah yang disetujui, bukan yang diminta

Jebakan termahal di modul ini:

> Yang masuk `kartu_stok` adalah `qty_disetujui_dasar`, bukan `qty_dasar`. Nilai 0 berarti item ditolak meski notanya disetujui.

| | Akibat |
|---|---|
| ❌ posting pakai `qty_dasar` | barang yang **tidak** jadi diberikan tetap dikeluarkan dari stok; selisihnya permanen karena `kartu_stok` append-only |
| ❌ `qty_disetujui_dasar` diisi saat draft | pemohon menyetujui permintaannya sendiri lewat pintu belakang |
| ✅ diisi saat `setujui`, default `qty_dasar` | penyetuju boleh memangkas per baris, 0 berarti baris itu ditolak sendirian |

Baris dengan `qty_disetujui_dasar = 0` **dilewati** saat posting, bukan ditulis sebagai baris kartu stok bernilai nol — tabel itu tidak boleh punya baris tanpa pergerakan. Kalau semua baris berakhir nol, posting ditolak 400 sepenuhnya: notanya disetujui tapi tidak ada apa pun yang keluar, dan itu penolakan, bukan pemakaian. `qty_dasar` sendiri tidak pernah ditimpa — selisih diminta-vs-disetujui adalah data perencanaan, dan menimpanya membuang satu-satunya jejak bahwa permintaan pernah dipangkas.

### Alur status: satu tahap lebih banyak dari `pembelian`, dan satu keputusan lebih permanen

```
DRAFT --ajukan--> DIAJUKAN --setujui--> DISETUJUI --posting--> POSTED --batal--> BATAL
                     \--tolak--> DITOLAK
```

Dua hal berbeda dari `pembelian`, keduanya disengaja:

- **`DITOLAK` itu terminal, tidak kembali ke `DRAFT`.** Di `pembelian`, penolakan berarti "kertasnya salah ketik, perbaiki" — makanya kembali ke `DRAFT`. Di sini penolakan adalah keputusan bisnis: barangnya tidak diberikan. Mengembalikannya ke `DRAFT` mengaburkan penolakan jadi revisi, dan menghapus satu-satunya jejak bahwa permintaan itu pernah ditolak. Kalau pemohon masih menginginkannya, ia mengajukan permintaan baru — dan itu memang yang seharusnya terlihat.
- **`DISETUJUI` terpisah dari `POSTED`, dan itu bukan *state* yang mubazir.** Persetujuan memutuskan *berapa yang boleh*; posting mencatat *barangnya benar-benar keluar*. Keduanya bisa beda hari — disetujui hari ini, diserahkan gudang besok — dan yang menentukan periode akuntansi adalah yang kedua. `ts_disetujui` dan `posted_at` dua kolom terpisah justru karena itu.

`disetujui_oleh`, `ts_disetujui`, dan `catatan_persetujuan` dipakai untuk **dua** keputusan, bukan satu — skemanya tidak punya kolom `ditolak_oleh` sendiri, jadi menolak pun mengisi kolom yang sama, dengan alasannya di `catatan_persetujuan`.

### Pemohon, pembuat, dan penyetuju adalah tiga orang berbeda

`id_pemohon` bukan `created_by`. Staf administrasi boleh mengetikkan permintaan atas nama montir yang tidak punya akun sendiri — `created_by` mencatat siapa yang mengetik, `id_pemohon` siapa yang meminta barangnya. `pemakaian_penyetuju_check` membandingkan `disetujui_oleh` dengan **`id_pemohon`**, bukan dengan `created_by`.

Pelanggarannya ditangkap di Go lebih dulu — di `Setujui` maupun `Tolak` — supaya pesannya menyebut sebabnya; CHECK-nya tetap bertahan sebagai jaring pengaman, hubungan yang sama seperti `ExistsByKode` dengan unique index-nya.

### Posting: nilai ditentukan trigger, aplikasi hanya membacanya kembali

Sama persis dengan sisi keluar `mutasi`, dan alasannya sama: rata-rata bergerak ruang hanya diketahui `kartu_stok_hitung_saldo()` di dalam advisory lock, dan trigger menimpa `nilai_keluar` serta `harga_pokok_satuan` pada setiap baris keluar. `hpp_satuan_dasar` dan `hpp_total` tiap baris — serta `total_hpp` header, jumlah seluruh `hpp_total` — baru terisi di sini, dari `RETURNING`, bukan dihitung Go.

**Ini modul kedua setelah `mutasi` yang stoknya benar-benar bisa kurang.** Penolakan trigger karena stok minus di sini kejadian sehari-hari, bukan pertahanan teoretis seperti di `retur_pembelian`, jadi pesannya menyebut produk dan ruangnya — sama seperti pemeriksaan saldo `mutasi`, dipakai hanya untuk kalimatnya, bukan penjaganya.

`KartuStokRepository.KunciSaldo` dipakai di sini juga, **meski hanya satu ruang per dokumen**: dua permintaan bersamaan yang menyebut produk sama dengan urutan baris berbeda tetap ABBA, karena trigger mengambil satu advisory lock per insert, bukan per dokumen. `TestDuaPemakaianBersamaanProdukSamaTidakDeadlock` mematoknya persis seperti `TestMutasiBerlawananArahTidakDeadlock` mematok kasus mutasi.

Pembatalan menulis baris pembalik dengan `jenis_transaksi = 'PEMBATALAN_TRANSAKSI'`, **bukan** `PEMBATALAN_PEMAKAIAN` — mengikuti keempat modul lain, bukan nilai enum yang menganggur sejak migrasi `000002` dan tidak bisa dibuang (`DROP VALUE` tidak ada di PostgreSQL). Berbeda dari `mutasi`, pembatalan di sini **tidak pernah bisa ditolak karena saldo minus**: ia hanya menambah stok kembali ke ruang asal, jadi tidak butuh pemeriksaan saldo sama sekali.

### Hal lain yang perlu diketahui

- **Tidak ada unique index `(id_pemakaian, id_product)`.** Mengikuti `mutasi`, bukan `penerimaan_susulan`/`retur_pembelian`/`pembayaran_utang`: di sana kuota dipegang baris induk sehingga dua baris bisa lolos sendiri-sendiri; di sini kuotanya saldo ruang, dijumlahkan per produk sebelum diperiksa dan diperiksa lagi trigger tiap insert. Dua baris produk sama dengan satuan berbeda — 1 DUS untuk bengkel, 3 PCS untuk kantor — permintaan yang sah.
- **Tidak ikut disaring unit_kerja aktif (isu #12).** Modul-modul lain yang menulis `kartu_stok` memvalidasi `id_ruang` terhadap unit aktif sesi pemanggil (fase 5) dan menyaring bacaannya (fase 6); isu #9 sendiri tidak memintanya untuk `pemakaian`, jadi `PemakaianUseCase` tidak membawa `RuangRepository` sama sekali.
- Nomornya seri sendiri, `PM/2026/08/0001`, dari generator yang sama.
- Tidak ada satu pun kolom uang di header selain `total_hpp` — tidak ada subtotal, diskon, PPN, ongkir, maupun koli.

## Penjualan: nota keluar, HPP dari trigger, dan awal sisi piutang

Nota keluar ke pembeli — isu #10. Dokumen keenam yang menulis `kartu_stok`, dan yang **pertama mengeluarkan barang ke pihak luar dengan uang di sisi lain**: `pembelian` membentuk utang, `mutasi` tidak membentuk apa-apa, ini yang pertama membentuk **piutang** — dan hanya pada nota `KREDIT`.

Tabel `penjualan`/`penjualan_detail` ada sejak migrasi `000006` dan nilai enum `PENJUALAN` sejak `000002`, jadi **tidak ada `ALTER TYPE` sama sekali** — seberuntung `mutasi` dan `pemakaian`. Migrasi `000022` hanya mengunci kosakata status/jenis pembayaran/status pembayaran lewat CHECK, menambah `penjualan_kredit_pelanggan_check` yang sebelumnya tidak pernah dijaga sama sekali, dan menambah indeks urutan baca.

### Jebakan utama: HPP datang dari trigger, bukan dari form

Sama persis dengan `mutasi` dan `pemakaian`: rata-rata bergerak sebuah ruang hanya diketahui `kartu_stok_hitung_saldo()` di dalam advisory lock, dan trigger menimpa `nilai_keluar` serta `harga_pokok_satuan` pada setiap baris keluar. `hpp_satuan_dasar` dan `hpp_total` tiap baris — serta `total_hpp` header, jumlah seluruh `hpp_total` — baru terisi saat posting, dari `RETURNING`, tidak pernah dihitung di Go. Begitu terisi, margin nota jadi gratis: `total - total_hpp`, tanpa tabel baru dan tanpa perhitungan ulang — itu satu-satunya alasan kolomnya ada.

**Ini modul kedua setelah `pemakaian` yang stoknya benar-benar sering tidak cukup**, bukan pertahanan teoretis seperti `retur_pembelian`: kasir mengetik 10 padahal tinggal 7 di toko, dan itu kejadian sehari-hari. Pesannya menyebut produk dan ruang, dan `KartuStokRepository.KunciSaldo` dipakai walau satu ruang per dokumen — dua nota bersamaan yang menyebut produk sama dengan urutan baris berbeda tetap ABBA, karena trigger mengambil satu advisory lock per insert, bukan per dokumen.

### Alur status: tanpa `DIAJUKAN`, tapi bukan karena taruhannya kecil

```
DRAFT --posting--> POSTED --batal--> BATAL
```

Sama seperti `mutasi`, tapi alasannya berbeda. Aturan untuk tiga penulis `kartu_stok` yang lebih dulu berbunyi: pisahkan penjaganya per tahap alur, karena posting yang salah hanya bisa dibalik. `mutasi` boleh membuang `DIAJUKAN` karena taruhannya kecil — barang tercatat di ruang keliru, total stok dan nilai tidak bergerak. **Penjualan tidak seperti itu**: barangnya betul-betul hilang dari toko, uang berpindah, dan kalau kredit maka piutang terbentuk atas nama orang lain.

Yang membuat `DIAJUKAN` tidak terjangkau adalah kendala praktis, bukan taruhan yang kecil: **kasir tidak bisa membuat pembeli menunggu persetujuan sambil berdiri di depan meja**. Nota tunai diketik dan diserahkan dalam hitungan detik. Jadi kendali dua orangnya pindah seluruhnya ke sisi pembatalan — `CASHIER` membuat, mengetik baris, dan memposting; `SUPERADMIN` satu-satunya yang boleh membatalkan. Kesalahan ketik dikoreksi dengan `retur_penjualan` (isu tersendiri, belum dibangun) atau pembatalan oleh atasan.

### `status_pembayaran` untuk nota tunai: turunan dari jenis dokumen, bukan dari alokasi

Aturannya sudah ditulis di `pembelian`: `status_pembayaran` adalah cache, selalu dihitung ulang dari alokasi dan retur POSTED, tidak pernah di-set dari form. Masalahnya nota tunai tidak punya alokasi — uangnya diterima di meja, tidak ada dokumen `penerimaan_pembayaran` yang menunjuknya. Jawabannya: **nota `TUNAI` yang `POSTED` dihitung `LUNAS` di dalam rumus itu sendiri**, turunan dari jenis dokumen, bukan kolom yang bisa diketik. Nota `KREDIT` tetap `BELUM` sampai modul `penerimaan-pembayaran` dibangun — sengaja tidak diseret ke isu ini, dan meninggalkan ribuan dokumen pembayaran yang tidak pernah dilihat siapa pun untuk setiap nota tunai bukan harga yang sepadan.

### `id_pelanggan` nullable, tapi `KREDIT` mewajibkannya

Nullable karena pembeli yang bayar tunai di depan meja tidak perlu didaftarkan — memaksanya berarti master `pelanggan` penuh baris "umum" yang tidak berarti apa-apa. Tapi piutang tanpa pelanggan tidak bisa ditagih siapa-siapa, dan `penjualan_kredit_pelanggan_check` yang menjaganya di database. Ditangkap di Go lebih dulu supaya pesannya menyebut fieldnya — hubungan yang sama seperti `ExistsByKode` dengan unique index-nya.

### Harga: usulan dari master, snapshot di nota

`harga_satuan_input` adalah yang ditagih, dan itu snapshot — seperti `faktor_konversi` di dokumen lain. `id_harga_jual` hanya mencatat versi daftar harga mana yang jadi asalnya, dan boleh kosong: produk yang belum punya harga tetap boleh dijual dengan harga ketik manual. Kalau diisi, **divalidasi**: harus milik `id_product`/`id_satuan_input` baris itu dan berlaku pada tanggal dokumen — lewat `FindHargaBerlakuBatch`, resolver batch yang memang dibangun untuk `penjualan` sejak isu #8 — lihat [Produk, satuan konversi, dan harga jual](#produk-satuan-konversi-dan-harga-jual). Harga yang diketik tidak pernah dipaksa sama dengan harga master; tawar-menawar terjadi, dan yang benar adalah yang tertulis di nota.

### Fase 2: piutang pelanggan dan `plafon_kredit`

`GET /api/v1/pelanggan/{id}/piutang` adalah **query yang bukan modul**, cerminan `GET /supplier/{id}/utang`: hidup di `penjualan_repository.go`, dipinjam `PelangganUseCase`, tanpa tabel dan tanpa migrasi. Hanya nota `KREDIT` yang `POSTED`, diurutkan paling lama dulu karena ini antrean tagihan. `sisa_piutang` sama dengan `total` untuk saat ini — belum ada `retur_penjualan` maupun `penerimaan_pembayaran` yang bisa menguranginya; begitu salah satunya ada, angka ini mulai memperhitungkannya tanpa bentuk responsnya berubah.

`plafon_kredit` ditegakkan di posting, di bawah row lock nota — draft bukan penjualan, jadi tidak diperiksa saat mengetik. Piutang berjalan pelanggan (jumlah `total` seluruh nota `KREDIT` `POSTED`-nya) ditambah nota yang sedang diposting tidak boleh melampaui `plafon_kredit`; `NULL` berarti tanpa batas dan tidak pernah menolak, arti yang sama di setiap tempat kolom ini dibaca. **Tidak ada jalur tembus untuk `SUPERADMIN`** — posting sudah di tangan `CASHIER` sendirian, dan menambah pembypass berarti menaruh aktor kedua justru di satu-satunya titik yang sengaja dijaga satu orang. Pemeriksaan ini juga satu-satunya di modul ini yang **tidak** punya CHECK atau trigger di belakangnya — tidak ada constraint yang bisa membandingkan batas dengan SUM yang berjalan — jadi ia sendiri penjaganya, dan tidak menutup race dua nota kredit yang diposting bersamaan untuk pelanggan yang sama.

### Hal lain yang perlu diketahui

- **Tidak ada unique index `(id_penjualan, id_product)`.** Mengikuti `mutasi`/`pemakaian`: kuotanya saldo ruang, dijumlahkan per produk sebelum diperiksa dan diperiksa lagi trigger tiap insert. Dua baris produk sama dengan satuan berbeda adalah nota yang sah.
- **Tidak ikut disaring unit_kerja aktif (isu #12).** Sama seperti `pemakaian`: isu #10 sendiri tidak memintanya, jadi `PenjualanUseCase` tidak membawa `RuangRepository` sama sekali.
- Nomornya seri sendiri, `PJ/2026/08/0001`, dari generator yang sama.
- Tidak ada aritmetika proporsional sama sekali — tidak ada `bagiProporsional` seperti `pembelian`, karena diskon nota tidak dibagi ke baris: HPP-nya datang dari trigger, bukan dari nilai per baris yang perlu dibentuk. Satu-satunya aritmetikanya penjumlahan dan pengurangan lewat `math/big.Rat`.
- Pembatalan menulis baris pembalik dengan `jenis_transaksi = 'PEMBATALAN_TRANSAKSI'`, bukan `PEMBATALAN_PENJUALAN` — pola yang sama seperti `mutasi`/`pemakaian`, dan `id_kartu_stok_asal` sudah cukup menjelaskan apa yang dibalik.
- Foto nota bertanda tangan bisa dilampirkan lewat `POST /api/v1/dokumen/{id}/tempel` dengan `ref_table = "penjualan"` — satu baris di `repository.RefTableDokumen`, tanpa migrasi.
- **Belum dijaga**, dan sengaja dicatat di sini supaya tidak terlewat: pembatalan seharusnya ditolak begitu ada `retur_penjualan` POSTED atau alokasi pembayaran yang menunjuk nota ini, persis `HasPostedRetur` di sisi pembelian — tapi belum ada modul yang menunjuk `penjualan` hari ini, jadi penjaganya belum bisa ditulis.

## Stok per ruang: bacaan pertama atas kartu stok

`GET /api/v1/product/{id}/stok`

`KartuStokRepository` sebelumnya cuma punya `Insert`, `FindByRef`, dan `HasRef` — **tidak ada satu pun cara membaca saldo**, dan sampai isu #7 memang tidak perlu: pembelian dan susulan hanya menambah, dan kuota retur datang dari baris faktur, bukan dari saldo.

Layar input mutasi adalah yang pertama membutuhkannya — orang harus tahu ruang asal punya berapa sebelum mengetik — dan sesudahnya `pemakaian` serta `penjualan` membutuhkan hal yang persis sama; stok opname nanti begitu juga. Karena itu ia dibangun sebagai fase tersendiri: `SaldoTerakhir`, `SaldoBatch`, dan `SaldoPerRuang` di repository, plus satu **query yang bukan modul** yang mengikuti bentuk riwayat harga beli — tanpa tabel, tanpa migrasi, tanpa DTO yang harus diisi.

- **Ini bacaan, bukan penjaga.** Angkanya bisa sudah bergerak sebelum klien menindaklanjutinya; yang memutuskan saldo adalah trigger, di dalam advisory lock, justru supaya tidak ada pembaca yang bisa mendahuluinya.
- **Pasangan `(barang, ruang)` tanpa baris sama sekali adalah saldo nol**, bukan baris yang hilang. Itu bacaan yang sama yang diambil trigger saat ia meng-`COALESCE` baris sebelumnya ke nol, dan bentuk yang sama yang dipakai `periode` untuk bulan yang belum pernah ditutup: ketiadaan adalah nilai, bukan 404.
- **Tidak ada paginasi**, berbeda dengan setiap daftar lain di API ini. Jawabannya satu baris per ruang yang pernah dilalui produk, `ruang` tabel master yang isinya sedikit, dan setiap pemakainya menginginkan semuanya sekaligus untuk memilih ruang asal.
- Ruang yang belum pernah dilalui tidak muncul; ruang yang pernah dilalui lalu habis tetap muncul dengan nol, karena itu fakta tentang ke mana barangnya pergi.
- Produk yang tidak dikenal menjawab 404, produk yang belum pernah bergerak menjawab array kosong. Keduanya fakta yang berbeda.

## Autentikasi

Seluruh `/api/v1` butuh bearer token, kecuali `POST /api/v1/auth/login`.

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:3000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin12345"}' | jq -r .data.token)

curl -s http://127.0.0.1:3000/api/v1/auth/me -H "Authorization: Bearer $TOKEN"
curl -s http://127.0.0.1:3000/api/v1/supplier -H "Authorization: Bearer $TOKEN"
```

**`JWT_SECRET` wajib diisi, minimal 32 karakter.** Server berhenti saat boot tanpa itu, dan `config.example.json` sengaja mengisinya dengan string kosong. Tidak ada default karena kunci default berarti kunci yang sama dipakai setiap deployment, dan siapa pun yang memegangnya bisa membuat token `SUPERADMIN` untuk user id mana pun. `docker compose` sudah memberi nilai dev supaya `up` langsung jalan; ganti lewat `.env`. Bangkitkan dengan `openssl rand -base64 48`.

> [!IMPORTANT]
> **Token tidak bisa dicabut.** Ini konsekuensi dari JWT stateless: tidak ada yang disimpan di server dan tidak ada lookup per request, jadi tidak ada apa pun yang bisa dibatalkan. Menonaktifkan user (`is_aktif: false`) atau mencabut rolenya **tidak** menyentuh token yang sudah keluar — aksesnya baru hilang saat token kedaluwarsa.
>
> Umur token karena itu adalah satu-satunya batas jendela tersebut. Defaultnya 60 menit (`JWT_TTL_MINUTES`), dan memperpanjangnya berarti memperpanjang jendela itu. Kalau pencabutan seketika dibutuhkan, sesi harus pindah ke Redis — Redis sudah terhubung tapi belum dipakai.

Grant ikut di dalam token, jadi otorisasi tidak menyentuh database. Efek sampingnya: **grant yang diberikan atau dicabut baru berlaku pada login berikutnya** (atau lewat `switch-context`, lihat di bawah). Hanya grant *usable* yang masuk token — rolenya `is_aktif`, dan kalau grant-nya menyebut unit, unitnya juga `is_aktif` — jadi mempensiunkan role atau unit menghentikannya memberi izin pada login berikutnya meski penugasannya masih tercatat.

### Konteks aktif per sesi (isu #12 fase 4)

Sejak wewenang bertempat ada (fase 3), satu user bisa memegang lebih dari satu grant sekaligus — kasir di outlet A, inventaris di outlet B. Sebuah token karena itu mengotorisasi sebagai **satu grant aktif**, bukan gabungan seluruhnya:

- **Memegang tepat satu grant usable memilihkannya otomatis.** Tidak ada langkah tambahan untuk kasus paling umum — satu user, satu peran, satu tempat.
- **Memegang lebih dari satu grant menerbitkan token tanpa konteks aktif** (`aktif: null`). Dengan grant bertempat, tidak ada default yang aman dipilihkan diam-diam — memilihkan salah satunya begitu saja adalah cara termudah membuat orang tidak sadar sedang bertindak sebagai apa, di outlet mana. Token seperti ini **tidak mengotorisasi apa pun**: setiap endpoint berrole guard menjawab 403, termasuk yang sebenarnya dipegang pemanggil lewat salah satu grant-nya.
- **`POST /api/v1/auth/switch-context`** `{"id_user_role": 12}` menukar token dengan satu yang beraksi sebagai grant itu. `id_user_role` datang dari `grants[].id_user_role` pada respons login atau `GET /auth/me` — id baris `user_role` itu sendiri, bukan id role.
- **Grant-nya diperiksa ulang ke database saat itu juga**, bukan dipercaya dari klaim token pemanggil — satu-satunya titik di seluruh desain ini di mana kebasian token dibiarkan berpengaruh. Grant yang bukan milik pemanggil, yang rolenya sudah dipensiunkan, atau yang unitnya sudah dipensiunkan, semuanya menjawab **403 yang sama** — membedakannya memberi jalan menebak id grant milik orang lain.
- **Token lama tetap sah.** Menukar konteks menerbitkan token baru; ia tidak — dan tidak bisa — mencabut yang lama. Konteks aktif adalah kendali kejelasan dan *least privilege*, bukan batas keamanan terhadap pemegang tokennya sendiri. Kalau jendela `JWT_TTL_MINUTES` dianggap terlalu lebar, itu yang disetel — bukan menambah blacklist Redis, yang mengembalikan lookup per request yang justru jadi alasan JWT dipilih.
- **Ini bukan `RequireRole` atau tabel rute yang berubah.** `switch-context` dan `auth/me` tidak berrole guard karena sesi tanpa konteks aktif memang tidak lolos `RequireRole` apa pun dengan sendirinya — `Session.HasRole` menjawab `false` kalau `Aktif` nil. Setiap route guard di `route.go` persis seperti sebelum fase 4.

### `id_ruang` divalidasi terhadap unit aktif (isu #12 fase 5)

`unit_kerja → ruang` itu satu-ke-banyak — satu outlet bisa punya gudang dan etalase sekaligus — jadi mengetahui unit aktif saja tidak cukup untuk menyimpulkan ruang mana yang dimaksud. Klien tetap memilih; yang berubah, `id_ruang` yang dikirim **divalidasi**, bukan sekadar dipakai apa adanya:

- **Hanya jalur tulis.** `POST /api/v1/pembelian` (field `id_ruang`) dan `POST`/`PATCH /api/v1/mutasi` (field `id_ruang_asal`) — dua-duanya satu-satunya tempat `id_ruang` datang langsung dari body. `penerimaan-susulan` dan `retur-pembelian` menyalin `id_ruang` dari `pembelian` induknya, jadi sudah tervalidasi lewat pembelian itu sendiri. Bacaan (list/get) tidak ikut disaring **di fase ini** — direkomendasikan begitu oleh isu ini untuk fase pertama, dan menutup lubang yang sebenarnya tanpa menyentuh belasan endpoint list. Fase 6 di bawah membangun penyaringan bacaan itu.
- **`mutasi` hanya memeriksa `id_ruang_asal`, tidak pernah `id_ruang_tujuan`.** Mutasi antar unit sudah diputuskan boleh (fase 1), dan konteks aktif selalu satu unit — mewajibkan kedua ruang cocok dengan unit itu berarti mutasi lintas-unit jadi mustahil, membalik keputusan itu. Yang diperiksa adalah wewenang atas ruang tempat barang dinyatakan **keluar**; ke mana barang itu pergi bukan klaim atas wewenang pemanggil sendiri.
- **Konteks aktif global (grant tanpa unit, bentuk `SUPERADMIN`) tidak dibatasi sama sekali.** `id_unit_kerja` kosong berarti berlaku di seluruh unit, jadi tidak ada yang perlu dicocokkan.
- **Di luar unit aktif → 403**, bukan 400: ini soal wewenang, bukan format data yang salah.
- `PATCH /api/v1/mutasi/{id}` hanya memeriksa ulang saat `id_ruang_asal` benar-benar dikirim di body. `ruang` sendiri tidak punya `PATCH`, jadi unitnya tidak mungkin berubah sejak dibuat — ruang yang tersimpan dan tidak disentuh patch tidak perlu diperiksa lagi.
- **Ini menutup lima fase yang wajib dibangun.** Fase 6 sendiri dibagi tiga bagian opsional oleh isu ini ("baru kalau terbukti perlu") — penyaringan bacaan, `users.id_ruang_default`, dan peran-sebagai-snapshot di dokumen. Hanya yang pertama diminta dan dibangun; lihat bagian berikutnya.

### Bacaan disaring per unit aktif (isu #12 fase 6)

Bagian opsional dari fase 6 yang diminta dan dibangun; dua lainnya (`users.id_ruang_default`, peran-sebagai-snapshot) tetap ditunda. `Get` dan `List` pada `ruang`, `pembelian`, `penerimaan-susulan`, `retur-pembelian`, dan `mutasi`, plus `GET /product/{id}/stok`, kini disaring oleh unit_kerja aktif sesi pemanggil — konteks aktif global tidak dibatasi, seperti pada fase 5.

- **Bentuk penyaringannya beda menurut bentuk bacaannya.** Sebuah `Get` di luar unit aktif menjawab **404**, bukan 403 — perilaku yang sama seperti id yang memang tidak ada, supaya jawabannya tidak diam-diam mengonfirmasi dokumennya benar ada. Sebuah `List` (dan `stok`, yang berbentuk daftar) diam-diam **melewati** baris di luar unit, tanpa error sama sekali — tidak ada id untuk dijawab 404, dan tidak ada apa pun untuk dikonfirmasi atau disangkal.
- **`mutasi` mewarisi asimetri yang sama dari fase 5, di sisi bacaan.** Hanya `id_ruang_asal` yang diperiksa; `id_ruang_tujuan` tidak pernah dibatasi. Dokumen yang tujuannya di unit lain tetap terlihat penuh bagi siapa pun yang memegang ruang asalnya — sebaliknya, pemanggil yang hanya memegang ruang tujuan tidak bisa melihatnya sama sekali, meski barangnya menuju ke sana. Ini pilihan yang disengaja: yang menentukan visibilitas adalah ruang yang wewenangnya sedang diklaim pemanggil, bukan setiap ruang yang disentuh dokumen.
- **`penerimaan-susulan` dan `retur-pembelian` tidak punya `id_ruang` sendiri untuk diperiksa** — keduanya menyalin ruang dari `pembelian` induknya — tapi bacaannya tetap perlu tahu unit ruang itu, jadi query baca kedua modul ikut menyertakan `ruang.id_unit_kerja` lewat join yang sudah ada.
- **`GET /product/{id}/stok` bukan `Get`, jadi tidak ada 404 untuk ruang yang di luar unit** — sama seperti `List`, baris ruangnya cuma dilewati.

### Superadmin pertama

Karena `POST /api/v1/user` hanya untuk `SUPERADMIN`, tanpa user awal API terkunci dari dirinya sendiri. `db/seeder_postgres/004_superadmin.sql` memasangnya:

| Username | Password | Role |
|---|---|---|
| `admin` | `admin12345` | `SUPERADMIN` |

Passwordnya ada di repositori, jadi perlakukan sebagai kredensial sekali pakai. Setelah login pertama:

```bash
# ganti passwordnya
curl -X PATCH http://127.0.0.1:3000/api/v1/user/1 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"password":"password-yang-panjang-dan-acak"}'
```

Atau buat superadmin sungguhan lalu nonaktifkan yang bawaan dengan `{"is_aktif": false}`.

### Otorisasi

Membaca terbuka untuk siapa pun yang sudah login — operator yang tidak bisa melihat supplier tidak bisa bekerja, apa pun rolenya. Ini soal route guard; sejak isu #12 fase 6, sebagian bacaan (lihat "Bacaan disaring per unit aktif" di atas) juga disaring lebih lanjut oleh unit_kerja aktif sesi pemanggil. Menulis dibagi menurut pemilik datanya. `SUPERADMIN` boleh apa saja.

| Resource | Baca | Tulis |
|---|---|---|
| `product`, `satuan`, `unit_kerja`, `ruang`, `ekspedisi`, `supplier` | semua yang login | `SUPERADMIN`, `INVENTARIS` |
| `pelanggan` | semua yang login | `SUPERADMIN`, `CASHIER` |
| `dokumen` (lampiran) | semua yang login | semua yang login |
| `pembelian`, `penerimaan_susulan`, `retur_pembelian` — input, edit, ajukan | semua yang login | `SUPERADMIN`, `INVENTARIS` |
| `pembelian`, `penerimaan_susulan`, `retur_pembelian` — posting, tolak, batal | — | `SUPERADMIN` |
| `pembayaran_utang` — input, edit, alokasi | semua yang login | `SUPERADMIN`, `CASHIER` |
| `pembayaran_utang` — posting, batal, cair, tolak giro | — | `SUPERADMIN` |
| `mutasi` — input, edit, ganti baris | semua yang login | `SUPERADMIN`, `INVENTARIS` |
| `mutasi` — posting, batal | — | `SUPERADMIN` |
| `pemakaian` — input, edit, ganti baris, ajukan | semua yang login | `SUPERADMIN`, `INVENTARIS` |
| `pemakaian` — setujui, tolak, posting, batal | — | `SUPERADMIN` |
| `penjualan` — input, edit, ganti baris, posting | semua yang login | `CASHIER` (`SUPERADMIN` juga bisa) |
| `penjualan` — batal | — | `SUPERADMIN` |
| `periode` (tutup buku) | semua yang login | `SUPERADMIN` |
| `role`, `user` | `SUPERADMIN` | `SUPERADMIN` |

`role` dan `user` tertutup termasuk untuk membaca: daftar akun beserta hak aksesnya sensitif, dan bisa menulis di sana adalah jalan eskalasi hak — beri diri sendiri `SUPERADMIN`, sisanya menyusul.

**`dokumen` tidak punya pembagian role sama sekali**, dan itu bukan aturan "baca terbuka" yang dilebarkan diam-diam ke tulis. Lampiran adalah infrastruktur milik banyak modul, jadi tidak ada satu pemilik data yang bisa ditunjuk. Yang menjaganya adalah **statusnya, bukan role pemanggilnya**: unggahan tidak berarti apa-apa sampai ada yang menempelkannya, penempelan ditolak begitu dokumen induknya `BATAL` atau sudah memegang 10 lampiran, dan penghapusan ditolak begitu induknya keluar dari `DRAFT`. Membagi role di sini berarti menentukan siapa yang boleh memotret faktur — pertanyaan yang sudah dijawab oleh siapa yang berdiri di meja penerimaan.

**`pembelian`, `penerimaan_susulan`, dan `retur_pembelian` dibagi menurut tahap alurnya, bukan menurut data yang disentuh** — ketiganya menulis `kartu_stok`, dan itulah alasannya. Pada `retur_pembelian` alasannya paling kuat: ia satu-satunya dokumen sejauh ini yang postingnya **mengeluarkan** barang, jadi yang salah bisa menekan saldo ke angka yang tidak lagi cocok dengan rak. Memposting pembelian bukan penyuntingan: ia menambah baris ke `kartu_stok` yang bersifat append-only, jadi posting yang salah tidak bisa diperbaiki, hanya dibalik — dan pembalikannya dinilai pada rata-rata bergerak yang sudah berubah. Karena itu meja yang membaca faktur kertas dan menghitung isi box bukan meja yang memutuskan angka-angka itu boleh masuk buku stok. Pembagiannya ada pada transisinya, bukan pada recordnya: di kantor kecil satu orang bisa saja memegang kedua role, dan baginya tidak ada yang berubah.

**`mutasi` menulis `kartu_stok` juga, tetapi tidak punya alur persetujuan — jadi baris di tabel ini adalah satu-satunya kendali yang tersisa.** Alurnya `DRAFT → POSTED → BATAL`, tanpa `DIAJUKAN`, karena taruhannya jauh lebih kecil: mutasi yang salah mencatat barang di ruang yang keliru sementara total stok dan total nilai persediaan tidak bergerak sedikit pun. Kendali dua orangnya tetap utuh — `INVENTARIS` menyiapkan, `SUPERADMIN` melepas — persis bentuk `pembayaran_utang`, satu state lebih sedikit. Konsekuensinya tidak ada lagi sinyal "draft ini sudah siap", dan penggantinya adalah daftar `status=DRAFT` dengan `terlama_dulu=true`.

**`pemakaian` dibagi menurut tahap alurnya juga, seperti `pembelian`, dan menambah satu tahap lagi.** `INVENTARIS` mengetik, mengedit, dan mengajukan; `SUPERADMIN` menyetujui (sekaligus memangkas per baris), menolak, memposting, dan membatalkan. Persetujuan dan posting sengaja dipisah menjadi dua aksi `SUPERADMIN`, bukan digabung — lihat [Pemakaian internal](#pemakaian-internal-permintaan-persetujuan-berjenjang-dan-posting) untuk kenapa keduanya bisa jatuh pada hari yang berbeda.

**`penjualan` menulis `kartu_stok` juga, tapi kendali dua orangnya pindah seluruhnya ke pembatalan — bukan ke posting seperti tiga dokumen di atas.** `CASHIER` membuat, mengedit, mengganti baris, **dan memposting** nota yang sama; `SUPERADMIN` satu-satunya yang boleh membatalkan. Kebalikan dari `mutasi`/`pemakaian` bukan karena taruhannya lebih kecil — justru lebih besar, karena barang benar-benar keluar dan untuk nota `KREDIT` piutang terbentuk — melainkan karena kendala meja kasir: pembeli tidak bisa menunggu persetujuan atasan untuk nota yang diketik dalam hitungan detik. Lihat [Penjualan](#penjualan-nota-keluar-hpp-dari-trigger-dan-awal-sisi-piutang) untuk alasan lengkapnya.

**Menulis `periode` hanya `SUPERADMIN`, dan itu penjagaan paling ketat di tabel ini.** Yang berubah saat sebuah bulan ditutup bukan data modul ini sendiri, melainkan kemampuan **setiap modul lain** memposting ke dalamnya. Membacanya tetap terbuka seperti pembacaan yang lain — apakah bulan lalu masih terbuka justru yang perlu diketahui operator sebelum mengetik faktur yang terlambat.

> [!NOTE]
> Pembagian di atas adalah **asumsi awal** yang ditarik dari tiga nama role, bukan hasil dari spesifikasi. Sesuaikan `setupAuthRoute` di `internal/delivery/http/route/route.go` begitu pembagian kerja sebenarnya jelas — seluruh kebijakannya ada di satu fungsi itu supaya bisa dibaca sekaligus.

Izin dihitung dari **gabungan** seluruh role: memegang salah satu role yang disyaratkan sudah cukup. Nama role dibandingkan tanpa memperhatikan huruf besar-kecil, karena `role.nama` unik tanpa memperhatikannya juga.

## Pengguna dan role

Satu user boleh punya banyak role, dan izinnya adalah **gabungan seluruh role** yang dipegang — tidak ada konsep "role yang sedang aktif". Role yang berlaku sekarang: `SUPERADMIN`, `CASHIER`, `INVENTARIS`.

Migrasi `000010` membetulkan bentuk lama, bukan sekadar menambah kolom:

- **`users.role_active` dibuang.** Migrasi `000002` memasang `UNIQUE (role_active)`, yang berlaku untuk seluruh tabel dan bukan per user — efek praktisnya satu sistem hanya bisa punya **satu kasir**, dan kasir kedua ditolak database. FK-nya juga menunjuk `user_role (id)` tanpa `user_id`, jadi role aktif user A bisa menunjuk penugasan milik user B. Kolomnya dihapus, tidak ditambal.
- Keunikan `username`, `email`, dan `role.nama` tidak peka huruf besar-kecil, lewat indeks `lower(...)` seperti kode master. `email` nullable, jadi banyak user tanpa email tetap boleh.
- `role` dapat `is_aktif` dan jejak perubahan; `user_role` dapat jejak kapan dan oleh siapa role diberikan.

Pemberian dan pencabutan lewat `grants` pada `POST`/`PATCH /api/v1/user`, bukan sub-resource tersendiri — supaya baris user dan pemberian grant-nya berada dalam satu transaksi. `grants` **mengganti seluruh himpunan**:

| Body | Artinya |
|---|---|
| tidak dikirim | grant dibiarkan apa adanya |
| `[]` | cabut semua grant |
| `[{"id_role":1}, {"id_role":3,"id_unit_kerja":2}]` | user berakhir dengan tepat grant yang disebut |
| `null` | ditolak 400 — `[]` sudah berarti "tanpa grant" |

**Isu #12 fase 3 — wewenang bertempat.** Sejak migrasi `000020`, satu grant bukan lagi cuma "role apa" tapi "role apa, di unit mana": `user_role.id_unit_kerja` menunjuk `unit_kerja`, dan `id_unit_kerja` kosong (`null`) berarti role itu berlaku di **seluruh** unit — bentuk grant `SUPERADMIN` bawaan. Konsekuensinya:

- **Role yang sama boleh dipegang di lebih dari satu unit**, sebagai dua baris `user_role` yang terpisah — "INVENTARIS di outlet A" dan "INVENTARIS di outlet B" bukan duplikat, dan keduanya boleh muncul sekaligus di `roles` seorang user.
- **Dua indeks unik, bukan satu**, karena indeks unik biasa tidak membatasi `NULL`: `user_role_grant_uidx` mengunci `(user_id, role_id, id_unit_kerja)`, dan `user_role_grant_global_uidx` — indeks parsial `WHERE id_unit_kerja IS NULL` — menutup celah yang pertama sendirian tidak bisa: tanpanya, grant lintas-unit yang sama bisa disisipkan berkali-kali untuk pasangan yang sama.
- **Diff-nya NULL-safe.** Mengganti himpunan grant membandingkan `(role_id, id_unit_kerja)` lewat `IS NOT DISTINCT FROM`, bukan `<>` biasa — PostgreSQL menganggap `NULL <> NULL` sebagai `NULL`, bukan benar, jadi perbandingan naif akan gagal mencabut grant lintas-unit yang seharusnya dicabut.
- **Konteks aktif per sesi** (fase 4, lihat [bagian Autentikasi](#autentikasi)): sebuah token kini mengotorisasi sebagai satu grant aktif, dipilih otomatis kalau cuma satu, atau lewat `POST /api/v1/auth/switch-context` kalau lebih.
- **`id_ruang` divalidasi terhadap unit aktif** (fase 5, lihat bagian yang sama): `pembelian` dan `mutasi` menolak 403 kalau ruangnya di luar unit_kerja aktif — kelima fase wajib isu #12 selesai di sini.
- **Bacaan disaring per unit aktif** (fase 6, opsional dan diminta terpisah): `Get`/`List` pada `ruang`, `pembelian`, `penerimaan-susulan`, `retur-pembelian`, `mutasi`, dan `GET /product/{id}/stok` disaring oleh unit_kerja aktif yang sama — 404 pada `Get`, baris dilewati diam-diam pada `List`/`stok`. `users.id_ruang_default` dan peran-sebagai-snapshot tetap ditunda.

Beberapa hal lain yang tidak terlihat dari daftar endpoint:

- **Password di-hash bcrypt** di lapisan usecase, tidak pernah disimpan, dicatat di log, atau dikembalikan. `UserResponse` sama sekali tidak punya field password, jadi kebocoran tidak mungkin secara struktural — bukan soal ingat atau tidak.
- **Grant yang tetap dipegang tidak dicabut lalu diberikan ulang**, sehingga `user_role.created_at` tetap mencatat kapan pemberian itu benar-benar dimulai.
- Body yang **hanya** berisi `grants` tetap menggerakkan `updated_at` user — itu tetap perubahan pada user tersebut.
- `id_role` yang tidak ada **atau sudah dipensiunkan** ditolak 400, dan begitu juga `id_unit_kerja`. Foreign key tidak bisa membedakan baris mati dari baris hidup, jadi pengecekannya di usecase — dan keduanya diperiksa terpisah, jadi pesannya menyebut yang mana yang salah.
- Daftar grant seorang user **termasuk yang rolenya, atau unitnya, dipensiunkan setelah diberikan** — pemberiannya masih nyata dan masih perlu dicabut. `is_aktif` di dalam `roles` yang membedakannya.
- Filter `role_id` di endpoint list memakai `EXISTS`, bukan join: join akan mengembalikan satu baris per role dan melipatgandakan halaman untuk user yang punya beberapa role.
- **`user_role` satu-satunya tabel yang boleh `DELETE`.** Tabel jembatan ini tidak dirujuk tabel transaksi mana pun, jadi mencabut grant tidak memutus foreign key dan tidak menghapus jejak dokumen — `created_by` di dokumen menunjuk `users`, bukan `user_role`.
- **Jangan ganti nama role yang sudah dipakai.** Nama role akan dipakai pengecekan izin begitu middleware otorisasi dibangun, dan tidak ada constraint database yang bisa mencegah nama diganti. Pensiunkan dengan `is_aktif: false` lalu buat role baru.

## Model data persediaan

Skema lengkap ada di migrasi `000002`–`000008`. **`pembelian`, `penerimaan_susulan`, `retur_pembelian`, `pembayaran_utang`, `mutasi`, `pemakaian`, dan `penjualan` sudah punya lapisan Go dan memakainya**; retur penjualan, penerimaan pembayaran, dan stok opname belum. Beberapa jaminan ditegakkan database, bukan aplikasi:

- **`kartu_stok` satu-satunya sumber kebenaran stok dan nilai persediaan.** Tidak ada kolom stok di tabel master, dan stok tidak pernah dihitung dengan menjumlahkan dokumen.
- **Append-only, dijaga trigger.** `UPDATE`, `DELETE`, dan `TRUNCATE` ditolak. Koreksi dilakukan lewat baris pembalik yang mengisi `id_kartu_stok_asal`.
- **Trigger yang menghitung saldo, bukan aplikasi.** `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, dan `nilai_akhir` ditimpa saat insert. Aplikasi hanya mengirim arah pergerakan (`stok_masuk` **atau** `stok_keluar`, tidak keduanya), `nilai_masuk`, dan kolom referensi.
- **Rata-rata bergerak**: barang masuk menggeser harga pokok, barang keluar tidak pernah. Stok nol memaksa nilai persediaan tepat nol supaya sisa pembulatan tidak menumpuk.
- Saldo dipartisi per `(id_barang, id_ruang)` dan diurutkan pakai `id`, bukan tanggal. Insert mengambil `pg_advisory_xact_lock` pada pasangan itu.
- Trigger menolak stok negatif dan posting ke `periode` berstatus `TUTUP`. Bulan tanpa baris `periode` dihitung terbuka. Sejak `000017` trigger juga mengambil advisory lock *shared* atas `(tahun, bulan)` sebelum membaca statusnya, supaya penutupan buku tidak bisa menyalip posting yang sedang berjalan — lihat [Tutup buku](#tutup-buku-periode-yang-menolak-posting).
- Kuantitas di `kartu_stok` selalu dalam satuan dasar; `qty_input`/`id_satuan_input` hanya jejak audit apa yang diketik operator.
- Dokumen menyimpan snapshot (harga, faktor konversi, HPP); master menyimpan aturan berjalan. Retur menyalin harga pokok dari baris dokumen asal, bukan dari rata-rata berjalan.
- Stok hanya bergerak saat posting, tidak saat draft.
- Piutang dan utang saling cermin, meski belum simetris implementasinya. `status_pembayaran` di `pembelian` adalah **cache** penuh, selalu dihitung ulang dari alokasi dan retur berstatus POSTED; giro belum `CAIR` bukan pembayaran, dan kelebihan bayar itu normal serta mengendap jadi kredit. `status_pembayaran` di `penjualan` juga cache — tidak pernah di-set dari form — tapi sisi piutangnya baru sebagian: nota `TUNAI` yang `POSTED` langsung `LUNAS` karena tidak pernah punya alokasi untuk dijumlahkan, sedangkan nota `KREDIT` tetap `BELUM` sampai `penerimaan_pembayaran` (cerminan `pembayaran_utang`) dibangun.
- **Biaya angkut dialokasikan per koli** (`metode_alokasi_angkut` default `'KOLI'` sejak migrasi `000008`), dengan fallback ke `QTY` bila semua `jumlah_koli` nol.

Penjelasan lengkap, termasuk aturan yang masih harus divalidasi di aplikasi, ada di [CLAUDE.md](CLAUDE.md).

## API

Kontrak di [`docs/openapi.yaml`](docs/openapi.yaml). Setiap perubahan route, request, atau response wajib tercermin di sana pada commit yang sama.

**Swagger UI ada di root**, <http://127.0.0.1:3000>, membaca kontrak yang disajikan di `/openapi.yaml`. Spec-nya ditanam ke dalam biner lewat `go:embed` di [`docs/docs.go`](docs/docs.go), jadi server tidak perlu berkas `openapi.yaml` di sebelahnya — tapi berarti `docs/` ikut jadi input build, dan menghapusnya dari konteks build Docker menggagalkan kompilasi, bukan sekadar menghilangkan halaman dokumentasi.

Matikan dengan `web.swagger: false` di `config.json`, atau `WEB_SWAGGER=false`. Saat dimatikan kedua rute tidak terdaftar sama sekali dan menjawab 404 — bukan halaman kosong, dan bukan `/openapi.yaml` yang tetap terbuka. Defaultnya menyala, termasuk untuk `config.json` yang ditulis sebelum kunci ini ada.

> [!NOTE]
> Tombol **Try it out** memanggil `servers:` di dalam kontrak, yaitu `http://127.0.0.1:3000`. Kalau dokumentasi dibuka dari host atau port lain, sesuaikan daftar itu — spec disajikan apa adanya dan tidak ada yang menulis ulangnya saat dikirim.
>
> Halaman Swagger UI-nya sendiri memuat aset dari CDN unpkg, jadi butuh koneksi internet untuk tampil. API di belakangnya tidak.

| Method | Path | Keterangan |
|---|---|---|
| `GET` | `/` | Swagger UI (kalau `web.swagger` menyala) |
| `GET` | `/openapi.yaml` | Kontrak OpenAPI, ditanam di biner |
| `GET` | `/health` | Liveness probe |
| `POST` | `/api/v1/auth/login` | Tukar kredensial dengan token — satu-satunya `/api/v1` tanpa token |
| `GET` | `/api/v1/auth/me` | Sesi yang sedang berlaku menurut token |
| `POST` | `/api/v1/auth/switch-context` | Tukar token dengan yang beraksi sebagai satu grant tertentu — tanpa role guard |
| `POST` | `/api/v1/dokumen` | Unggah lampiran (`multipart/form-data`, field `berkas`); barisnya lahir yatim |
| `GET` | `/api/v1/dokumen` | Lampiran satu dokumen (`ref_table` + `ref_id`), atau berkas yatim milik sendiri |
| `GET` | `/api/v1/dokumen/{id}` | Unduh isinya — selalu `attachment`, tetap butuh token |
| `POST` | `/api/v1/dokumen/{id}/tempel` | Tempelkan ke dokumen induk — `ref_table`, `ref_id` |
| `DELETE` | `/api/v1/dokumen/{id}` | Soft delete — hanya selama yatim atau induknya `DRAFT` |
| `GET` | `/api/v1/product` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/product` | Create, sekalian satuan konversinya |
| `GET` | `/api/v1/product/{id}` | Detail, dengan satuan dan riwayat harga jual |
| `PATCH` | `/api/v1/product/{id}` | Update `nama`, `stok_minimum`, `is_aktif` |
| `POST` | `/api/v1/product/{id}/satuan` | Tambah satuan konversi |
| `GET` | `/api/v1/product/{id}/harga-jual` | Versi harga yang berlaku per satuan pada satu `tanggal` (default hari ini WIB) |
| `POST` | `/api/v1/product/{id}/harga-jual` | Buka versi harga jual baru |
| `PATCH` | `/api/v1/product/{id}/harga-jual/{id_harga}` | Koreksi `harga` saja; ditolak 409 kalau versinya sudah dipakai dokumen |
| `DELETE` | `/api/v1/product/{id}/harga-jual/{id_harga}` | Hapus keras dan buka kembali versi sebelumnya; ditolak 409 kalau sudah dipakai dokumen |
| `GET` | `/api/v1/product/harga-jual` | Daftar harga lintas produk — `page`, `size`, `search`, `tanggal`, `is_aktif`; `LEFT JOIN`, produk tanpa harga tetap muncul |
| `GET` | `/api/v1/product/{id}/riwayat-beli` | Harga beli terakhir per supplier — `page`, `size`, `id_supplier` |
| `GET` | `/api/v1/product/{id}/stok` | Saldo dan nilai per ruang; tanpa paginasi |
| `GET` | `/api/v1/pembelian` | List — `page`, `size`, `search`, `status`, `status_penerimaan`, `id_supplier`, `tanggal_dari`, `tanggal_sampai` |
| `POST` | `/api/v1/pembelian` | Buat draft beserta barisnya; nomor digenerate server; `id_ruang` divalidasi terhadap unit aktif |
| `GET` | `/api/v1/pembelian/{id}` | Detail beserta baris, selisih, dan alokasi ongkir |
| `PATCH` | `/api/v1/pembelian/{id}` | Ubah header — hanya saat `DRAFT` |
| `PUT` | `/api/v1/pembelian/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` |
| `POST` | `/api/v1/pembelian/{id}/bagi-rata-koli` | Bagi `total_koli` proporsional ke `qty_dasar` |
| `POST` | `/api/v1/pembelian/{id}/ajukan` | `DRAFT` → `DIAJUKAN` |
| `POST` | `/api/v1/pembelian/{id}/posting` | Hitung alokasi, tulis `kartu_stok`, set `POSTED` — `SUPERADMIN` |
| `POST` | `/api/v1/pembelian/{id}/tolak` | `DIAJUKAN` → `DRAFT`, wajib `alasan` — `SUPERADMIN` |
| `POST` | `/api/v1/pembelian/{id}/batal` | Tulis baris pembalik, wajib `alasan_batal` — `SUPERADMIN` |
| `GET` | `/api/v1/pembelian/{id}/sisa` | Baris yang belum lengkap diterima |
| `GET` | `/api/v1/penerimaan-susulan` | List — `page`, `size`, `search`, `status`, `id_pembelian`, `tanggal_dari`, `tanggal_sampai` |
| `POST` | `/api/v1/penerimaan-susulan` | Buat draft; pembelian asal harus `POSTED` |
| `GET` | `/api/v1/penerimaan-susulan/{id}` | Detail beserta barisnya |
| `PATCH` | `/api/v1/penerimaan-susulan/{id}` | Ubah `tanggal`/`keterangan` — hanya saat `DRAFT` |
| `PUT` | `/api/v1/penerimaan-susulan/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` |
| `POST` | `/api/v1/penerimaan-susulan/{id}/ajukan` | `DRAFT` → `DIAJUKAN` |
| `POST` | `/api/v1/penerimaan-susulan/{id}/posting` | Tulis `kartu_stok`, hitung ulang `status_penerimaan` — `SUPERADMIN` |
| `POST` | `/api/v1/penerimaan-susulan/{id}/tolak` | `DIAJUKAN` → `DRAFT`, wajib `alasan` — `SUPERADMIN` |
| `POST` | `/api/v1/penerimaan-susulan/{id}/batal` | Tulis baris pembalik, kembalikan sisa — `SUPERADMIN` |
| `GET` | `/api/v1/retur-pembelian` | List — `page`, `size`, `search`, `status`, `id_pembelian`, `id_supplier`, `tanggal_dari`, `tanggal_sampai` |
| `POST` | `/api/v1/retur-pembelian` | Buat draft; pembelian asal harus `POSTED`, `alasan` wajib |
| `GET` | `/api/v1/retur-pembelian/{id}` | Detail beserta barisnya |
| `PATCH` | `/api/v1/retur-pembelian/{id}` | Ubah `tanggal`/`alasan` — hanya saat `DRAFT` |
| `PUT` | `/api/v1/retur-pembelian/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` |
| `POST` | `/api/v1/retur-pembelian/{id}/ajukan` | `DRAFT` → `DIAJUKAN` |
| `POST` | `/api/v1/retur-pembelian/{id}/posting` | Keluarkan dari `kartu_stok`, set `POSTED` — `SUPERADMIN` |
| `POST` | `/api/v1/retur-pembelian/{id}/tolak` | `DIAJUKAN` → `DRAFT`, wajib `alasan` — `SUPERADMIN` |
| `POST` | `/api/v1/retur-pembelian/{id}/batal` | Tulis baris pembalik, barang kembali masuk — `SUPERADMIN` |
| `GET` | `/api/v1/pembayaran-utang` | List — `page`, `size`, `search`, `status`, `metode`, `status_giro`, `id_supplier`, `tanggal_dari`, `tanggal_sampai` |
| `POST` | `/api/v1/pembayaran-utang` | Buat draft beserta alokasinya; alokasi boleh kosong |
| `GET` | `/api/v1/pembayaran-utang/{id}` | Detail beserta alokasinya |
| `PATCH` | `/api/v1/pembayaran-utang/{id}` | Ubah header — hanya saat `DRAFT` |
| `PUT` | `/api/v1/pembayaran-utang/{id}/alokasi` | Ganti seluruh alokasi — hanya saat `DRAFT` |
| `POST` | `/api/v1/pembayaran-utang/{id}/posting` | Bekukan alokasi, hitung ulang `status_pembayaran` — `SUPERADMIN` |
| `POST` | `/api/v1/pembayaran-utang/{id}/batal` | Kembalikan utangnya, wajib `alasan_batal` — `SUPERADMIN` |
| `POST` | `/api/v1/pembayaran-utang/{id}/cair` | Giro cair — di sinilah utang giro berkurang — `SUPERADMIN` |
| `POST` | `/api/v1/pembayaran-utang/{id}/tolak-giro` | Giro ditolak bank; utangnya tidak pernah berkurang — `SUPERADMIN` |
| `GET` | `/api/v1/mutasi` | List — `page`, `size`, `search`, `status`, `id_ruang_asal`, `id_ruang_tujuan`, `tanggal_dari`, `tanggal_sampai`, `terlama_dulu` |
| `POST` | `/api/v1/mutasi` | Buat draft; `detail` boleh kosong, kedua ruang wajib berbeda; `id_ruang_asal` divalidasi terhadap unit aktif |
| `GET` | `/api/v1/mutasi/{id}` | Detail beserta barisnya |
| `PATCH` | `/api/v1/mutasi/{id}` | Ubah header termasuk kedua ruang — hanya saat `DRAFT`; `id_ruang_asal` baru divalidasi terhadap unit aktif kalau dikirim |
| `PUT` | `/api/v1/mutasi/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` |
| `POST` | `/api/v1/mutasi/{id}/posting` | Tulis 2×N baris `kartu_stok`, set `POSTED` — `SUPERADMIN` |
| `POST` | `/api/v1/mutasi/{id}/batal` | Baris pembalik ke dua arah, wajib `alasan_batal` — `SUPERADMIN` |
| `GET` | `/api/v1/pemakaian` | List — `page`, `size`, `search`, `status`, `id_ruang`, `id_pemohon`, `tanggal_dari`, `tanggal_sampai`, `terlama_dulu` |
| `POST` | `/api/v1/pemakaian` | Buat draft; `detail` boleh kosong |
| `GET` | `/api/v1/pemakaian/{id}` | Detail beserta barisnya |
| `PATCH` | `/api/v1/pemakaian/{id}` | Ubah header — hanya saat `DRAFT` |
| `PUT` | `/api/v1/pemakaian/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` |
| `POST` | `/api/v1/pemakaian/{id}/ajukan` | `DRAFT` → `DIAJUKAN` |
| `POST` | `/api/v1/pemakaian/{id}/setujui` | `DIAJUKAN` → `DISETUJUI`; `detail` opsional per baris memangkas `qty_disetujui_dasar` — `SUPERADMIN` |
| `POST` | `/api/v1/pemakaian/{id}/tolak` | `DIAJUKAN` → `DITOLAK`, terminal, wajib `alasan` — `SUPERADMIN` |
| `POST` | `/api/v1/pemakaian/{id}/posting` | Tulis `kartu_stok` dari `qty_disetujui_dasar`, set `POSTED` — `SUPERADMIN` |
| `POST` | `/api/v1/pemakaian/{id}/batal` | Baris pembalik, wajib `alasan_batal` — `SUPERADMIN` |
| `GET` | `/api/v1/penjualan` | List — `page`, `size`, `search`, `status`, `status_pembayaran`, `jenis_pembayaran`, `id_ruang`, `id_pelanggan`, `tanggal_dari`, `tanggal_sampai` |
| `POST` | `/api/v1/penjualan` | Buat draft; `detail` boleh kosong; `KREDIT` wajib `id_pelanggan` — `CASHIER` |
| `GET` | `/api/v1/penjualan/{id}` | Detail beserta barisnya |
| `PATCH` | `/api/v1/penjualan/{id}` | Ubah header — hanya saat `DRAFT` — `CASHIER` |
| `PUT` | `/api/v1/penjualan/{id}/detail` | Ganti seluruh baris — hanya saat `DRAFT` — `CASHIER` |
| `POST` | `/api/v1/penjualan/{id}/posting` | Tulis `kartu_stok`, isi HPP, tegakkan `plafon_kredit` untuk KREDIT — `CASHIER` |
| `POST` | `/api/v1/penjualan/{id}/batal` | Baris pembalik, wajib `alasan_batal` — `SUPERADMIN` |
| `GET` | `/api/v1/satuan` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/satuan` | Create |
| `GET` | `/api/v1/satuan/{id}` | Get by id |
| `PATCH` | `/api/v1/satuan/{id}` | Update parsial |
| `GET` | `/api/v1/ekspedisi` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/ekspedisi` | Create |
| `GET` | `/api/v1/ekspedisi/{id}` | Get by id |
| `PATCH` | `/api/v1/ekspedisi/{id}` | Update parsial |
| `GET` | `/api/v1/supplier` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/supplier` | Create |
| `GET` | `/api/v1/supplier/{id}` | Get by id |
| `PATCH` | `/api/v1/supplier/{id}` | Update parsial |
| `GET` | `/api/v1/supplier/{id}/utang` | Faktur yang masih terbuka — `page`, `size`, `termasuk_lunas` |
| `GET` | `/api/v1/pelanggan` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/pelanggan` | Create |
| `GET` | `/api/v1/pelanggan/{id}` | Get by id |
| `GET` | `/api/v1/pelanggan/{id}/piutang` | Nota KREDIT yang masih terbuka — `page`, `size` |
| `PATCH` | `/api/v1/pelanggan/{id}` | Update parsial |
| `GET` | `/api/v1/periode` | List baris tersimpan — `page`, `size`, `tahun`, `status` |
| `GET` | `/api/v1/periode/{tahun}/{bulan}` | Status satu bulan; bulan tanpa baris menjawab `BUKA` sintetis |
| `POST` | `/api/v1/periode/{tahun}/{bulan}/tutup` | Tutup buku bulan itu |
| `POST` | `/api/v1/periode/{tahun}/{bulan}/buka` | Buka kembali |
| `GET` | `/api/v1/unit-kerja` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/unit-kerja` | Create |
| `GET` | `/api/v1/unit-kerja/{id}` | Get by id |
| `PATCH` | `/api/v1/unit-kerja/{id}` | Update parsial |
| `GET` | `/api/v1/ruang` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/ruang` | Create; `id_unit_kerja` wajib dan harus unit yang `is_aktif` |
| `GET` | `/api/v1/ruang/{id}` | Get by id |
| `GET` | `/api/v1/role` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/role` | Create |
| `GET` | `/api/v1/role/{id}` | Get by id |
| `PATCH` | `/api/v1/role/{id}` | Update parsial |
| `GET` | `/api/v1/user` | List — `page`, `size`, `search`, `is_aktif`, `role_id` |
| `POST` | `/api/v1/user` | Create, sekalian memberi grant (role + unit_kerja opsional) |
| `GET` | `/api/v1/user/{id}` | Get by id |
| `PATCH` | `/api/v1/user/{id}` | Update parsial, termasuk mengatur ulang grant |

`ruang` belum punya PATCH, jadi unitnya tidak bisa diganti lewat API. `page` default 1, `size` default 20 dengan batas 100. **Satu-satunya `DELETE` ada di `dokumen`, dan itu pun soft delete** — barisnya bertahan dengan `deleted_at` terisi, yang hilang cuma berkasnya. Selebihnya tidak ada penghapusan: master data dipensiunkan dengan `is_aktif = false`, dokumen yang sudah diposting dibatalkan dengan baris pembalik.

Semua respons memakai satu amplop, `model.WebResponse[T]`:

```json
{
  "data": { "...": "..." },
  "paging": { "page": 1, "size": 20, "total_item": 5, "total_page": 1 },
  "errors": "supplier not found",
  "validation_errors": { "Nama": "required" }
}
```

`data` untuk list selalu array JSON, tidak pernah `null` — begitu juga `roles` di dalam setiap user, jadi `roles.length` aman dibaca di setiap baris. Uang (`plafon_kredit`) dikirim sebagai string desimal supaya tidak ada float yang menyentuhnya; `null` berarti tanpa plafon, bukan nol.

## Status & Roadmap

Sudah ada:

- Skema database lengkap: master data, kartu stok beserta trigger saldo, pembelian, penjualan, piutang, utang, pemakaian, mutasi, stok opname
- **Mesin posting `kartu_stok`** dan **generator nomor dokumen lintas modul**, keduanya dibangun untuk dipakai ulang — dan setelah `mutasi` sudah lengkap, termasuk untuk pergerakan dua arah dalam satu dokumen
- **Modul `pembelian` penuh**: draft, edit, ganti baris, bagi rata koli, ajukan, tolak, posting, batal, dan daftar sisa — dengan dua kolom kuantitas, alokasi ongkir per koli yang berjumlah persis, dan nilai masuk yang proporsional terhadap yang benar-benar datang
- **Modul `penerimaan_susulan`** untuk kiriman yang menyusul: menambah stok tanpa menambah utang, harga pokok disalin dari baris pembelian sehingga satu faktur menyumbang persis nilainya sendiri ke persediaan, dan sisa per baris yang diperiksa ulang di bawah row lock saat posting
- **Modul `retur_pembelian`** untuk barang yang dikirim balik: cermin dari penerimaan susulan, harga pokok disalin dari baris pembelian sehingga pembelian dan returnya saling menghapus, dan kuota yang dibatasi pada barang yang **benar-benar datang** — bukan yang difakturkan
- **Riwayat harga beli per produk per supplier** — pengganti purchase order, tanpa dokumen tambahan yang harus diinput
- **Modul `pembayaran_utang` beserta alokasinya** — fase terakhir isu #4: satu pembayaran menutup banyak faktur dan sebaliknya, giro yang baru mengurangi utang saat cair, kelebihan bayar yang mengendap jadi kredit, dan `status_pembayaran` yang dihitung ulang dari alokasi efektif serta kredit retur
- **Pengurangan utang oleh retur**, lewat `nilai_kredit_utang` yang dibekukan saat posting — diskalakan terhadap `pembelian.total` supaya diskon nota tidak ikut dikreditkan dua kali, dan tidak memakai `retur_pembelian.total` yang sudah memuat porsi ongkir
- **Daftar utang per supplier** (`GET /supplier/{id}/utang`) — query, bukan modul, seperti riwayat harga beli
- **Subsistem lampiran berkas** (isu #5): unggah dengan MIME dari isi berkas, penyimpanan di balik interface, unduhan yang selalu `attachment` di balik token, penempelan polimorfik ke dokumen mana pun yang terdaftar, dan **job pertama di `cmd/worker`** — sapuan berkas yatim di bawah advisory lock, menghapus berdasarkan baris tabel dan tidak pernah dengan memindai direktori
- **Modul `periode`** (isu #6): tutup buku bulanan yang menolak posting ke bulan tertutup untuk setiap modul yang menulis `kartu_stok`, sekarang maupun nanti — dengan advisory lock yang membuat menutup dan memposting tidak bisa saling menyalip, pembukaan kembali yang meninggalkan jejaknya sendiri, dan pembalikan yang tetap bisa dibukukan di periode berjalan
- **Modul `mutasi`** (isu #7): perpindahan antar ruang sebagai satu dokumen yang menulis dua baris `kartu_stok` per baris detail, dengan nilai yang masuk ke ruang tujuan persis sebesar yang tercatat keluar dari ruang asal — sehingga total nilai persediaan tidak bergerak hanya karena barang pindah rak — alur tanpa `DIAJUKAN`, dan penguncian saldo di muka dalam urutan kanonik yang membuat dua mutasi berlawanan arah tidak bisa saling deadlock
- **Bacaan saldo `kartu_stok`** (`SaldoTerakhir`, `SaldoBatch`, `SaldoPerRuang`) beserta `GET /product/{id}/stok` — yang pertama membaca kembali kartu stok, dan sejak itu dipakai ulang `pemakaian`, lalu `penjualan`, dan nanti stok opname
- **Modul `pemakaian`** (isu #9): permintaan pemakaian internal dengan persetujuan berjenjang — `DRAFT → DIAJUKAN → DISETUJUI → POSTED`, plus `DITOLAK` yang terminal — yang diposting adalah `qty_disetujui_dasar` per baris, bukan yang diminta, baris yang disetujui nol dilewati begitu saja, dan `KunciSaldo` dipakai walau satu ruang per dokumen karena dua permintaan bersamaan atas produk yang sama tetap bisa ABBA
- **Modul `penjualan`** (isu #10): nota keluar sebagai dokumen keenam yang menulis `kartu_stok`, dan yang pertama membentuk piutang — HPP tiap baris dan `total_hpp` disalin dari `RETURNING kartu_stok`, bukan dihitung ulang; alur tanpa `DIAJUKAN` seperti `mutasi`, tapi karena kendala meja kasir, bukan kecilnya taruhan, sehingga kendali dua orangnya pindah ke pembatalan (`CASHIER` posting, `SUPERADMIN` batal); nota `KREDIT` mewajibkan `id_pelanggan` (`penjualan_kredit_pelanggan_check`) dan `id_harga_jual` opsional per baris divalidasi lewat `FindHargaBerlakuBatch`; fase 2-nya menambah `GET /pelanggan/{id}/piutang` (query, bukan modul, cerminan `GET /supplier/{id}/utang`) dan menegakkan `plafon_kredit` di posting
- **Modul `unit_kerja`** (isu #12 fase 1–2): lokasi organisasi tempat setiap `ruang` bernaung, `id_unit_kerja` wajib dan divalidasi aktif saat sebuah ruang dibuat, dan tiga keputusan yang ditulis sebelum kodenya — seri `document_counter` per unit (implementasinya ditunda), `periode` tetap global, dan `mutasi` antar unit diperbolehkan
- **Wewenang bertempat** (isu #12 fase 3): `user_role.id_unit_kerja`, dua indeks unik (satu penuh, satu parsial untuk grant lintas-unit), `ReplaceRoles` yang mendiff pasangan `(role, unit)` dengan `IS NOT DISTINCT FROM`, dan `grants` menggantikan `role_ids` di `POST`/`PATCH /api/v1/user`
- **Konteks aktif per sesi** (isu #12 fase 4): sebuah token mengotorisasi sebagai satu grant, dipilih otomatis kalau tepat satu dipegang, lewat `POST /api/v1/auth/switch-context` kalau lebih. Grant-nya diperiksa ulang ke database saat menukar — bukan dipercaya dari token pemanggil — dan setiap kegagalan (bukan milik pemanggil, role pensiun, unit pensiun, tidak ada) menjawab 403 yang sama. Token lama tetap sah sampai kedaluwarsa; menukar konteks tidak mencabutnya. `RequireRole` dan `route.go` tidak berubah satu baris pun — `Session.HasRole` yang menjawab `false` saat konteks belum dipilih sudah cukup
- **`id_ruang` divalidasi terhadap unit aktif** (isu #12 fase 5, penutup lima fase wajib): `POST /api/v1/pembelian` dan `POST`/`PATCH /api/v1/mutasi` (hanya `id_ruang_asal`) menolak 403 kalau ruangnya di luar unit_kerja aktif sesi pemanggil. Konteks global (tanpa unit) tidak dibatasi; `id_ruang_tujuan` pada `mutasi` tidak pernah dibatasi karena mutasi antar unit sengaja diperbolehkan; bacaan sengaja tidak ikut disaring di fase ini — rekomendasi isu ini untuk fase pertama
- **Bacaan disaring per unit aktif** (isu #12 fase 6, bagian opsional yang diminta): `Get`/`List` pada `ruang`, `pembelian`, `penerimaan-susulan`, `retur-pembelian`, `mutasi`, dan `GET /product/{id}/stok` disaring oleh unit_kerja aktif sesi pemanggil — `Get` menjawab 404 di luar unit, `List`/`stok` melewati baris di luar unit tanpa error. `mutasi` mewarisi asimetri fase 5: hanya `id_ruang_asal` diperiksa. `users.id_ruang_default` dan peran-sebagai-snapshot di dokumen tetap ditunda, sesuai isu ini
- **Harga jual siap pakai** (isu #8): `GET /product/{id}/harga-jual` menjawab versi yang **berlaku** pada satu tanggal per satuan — sebuah `WHERE`, bukan "yang terbaru menang", karena `product_harga_jual_no_overlap` menjamin tidak pernah ada dua versi berlaku bersamaan; resolver batch (`FindHargaBerlakuBatch`) yang saat ditulis disiapkan untuk `penjualan`, dan sejak isu #10 memang dipakainya untuk memvalidasi `id_harga_jual` opsional di setiap baris nota; `PATCH`/`DELETE .../harga-jual/{id_harga}` mengoreksi atau menghapus satu versi, ditolak 409 begitu dipakai `penjualan_detail`, dan `DELETE`-nya **keras** — pengecualian ketiga setelah `user_role` dan `dokumen` — sekaligus selalu membuka kembali versi sebelumnya supaya tidak ada rentang tanggal tanpa harga; `GET /product/harga-jual` adalah daftar harga lintas produk lewat `LEFT JOIN`, supaya produk tanpa harga tetap kelihatan; pemotongan tanggal-dari-timestamp memakai WIB (`Asia/Jakarta`, offset tetap +7) sejak `tanggalHargaJual`, dipakai saat `tanggal` query param GET diabaikan — `penjualan.tanggal` sendiri sudah tanggal murni (`YYYY-MM-DD`) sehingga masuk ke resolver batch apa adanya, tanpa pemotongan zona waktu
- Delapan modul lengkap sampai OpenAPI: `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `unit_kerja`, `role`, `user` (create/get/list/patch) dan `ruang` (create/get/list)
- User dengan banyak grant (role + unit_kerja opsional), `grants` yang mengganti seluruh himpunan dalam satu transaksi dengan diff `NULL`-safe, password ter-hash bcrypt
- Semantik PATCH dengan `model.Optional[T]`, keunikan kode tidak peka huruf, pemetaan pelanggaran unik jadi 409, escaping wildcard pencarian
- Autentikasi bearer JWT, otorisasi berbasis role per route, konteks aktif per sesi dengan `switch-context` (isu #12 fase 4), `id_ruang` divalidasi terhadap unit aktif (fase 5), bacaan disaring per unit aktif (fase 6), dan superadmin bawaan dari seeder
- Modul `product` beserta satuan konversi dan harga jual berversi, dengan satuan dasar otomatis dan periode harga yang tidak boleh tumpang tindih
- Wiring aplikasi penuh, graceful shutdown, penanganan error terpusat
- Test: unit test untuk `Optional`, validator, `EscapeLike`, dan amplop response; test usecase melawan PostgreSQL sungguhan

Belum ada:

- **Pengisian `created_by`/`updated_by` di modul master.** `product` dan `pembelian` mengambilnya dari token lewat `middleware.SessionFrom`; slice master lainnya masih menulis `NULL` meski jalurnya sudah ada
- **Pencabutan sesi.** Token stateless tidak bisa dicabut sebelum kedaluwarsa
- **Logout dan refresh token**
- Captcha (Redis sudah terhubung tapi belum dipakai)
- Lapisan Go untuk retur penjualan dan stok opname. **Mesin postingnya sudah lengkap** setelah `mutasi` dan `pemakaian`; `penjualan` (isu #10) sudah berjalan, tapi `retur_penjualan` menunjuk baris `penjualan_detail` yang baru ada sejak modul itu jadi, sehingga wajib dikerjakan sesudahnya
- **Penyimpanan lampiran di object storage.** Yang berjalan disk lokal di balik `repository.DokumenStorage`, jadi `web` belum bisa discale lebih dari satu instance
- Validasi tingkat aplikasi yang tersisa di sisi penjualan — **plafon kredit sudah ditegakkan** (isu #10 fase 2), sisanya belum: kuota retur penjualan, batas alokasi penerimaan pembayaran, dan `penjualan.status_pembayaran` yang baru dihitung penuh (bukan cuma TUNAI→LUNAS/KREDIT→BELUM) begitu `penerimaan_pembayaran` ada — sisi utang sudah selesai di fase 6, dan cerminnya tinggal ditiru. Didaftar lengkap di CLAUDE.md
- Job rekonsiliasi harian rantai saldo kartu stok
