# GRAND-ERP

Backend ERP dengan fokus pada persediaan, pembelian, dan penjualan. Ditulis dengan Go + Fiber v3 di atas PostgreSQL, tanpa ORM.

> **Status: master data, pengguna, dan produk berjalan; transaksi belum.** Delapan modul sudah punya kode Go lengkap dari migrasi sampai OpenAPI — `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `ruang`, `role`, `user`, dan `product` (beserta `product_satuan` dan `product_harga_jual`). Skema persediaan/pembelian/penjualan sudah termigrasi tetapi belum punya satu pun lapisan Go. Lihat [Status & Roadmap](#status--roadmap).

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

curl http://localhost:3000/health
```

Lalu buka **<http://localhost:3000>** — root menyajikan Swagger UI untuk seluruh API.

Itu menaikkan PostgreSQL 17, Redis 7, menjalankan migrasi, memasang seeder, lalu menjalankan server HTTP dan worker. Urutannya **ditegakkan**, bukan diharapkan — `postgres` harus lolos healthcheck sebelum `migrate` jalan, `migrate` harus selesai sebelum `seed`, dan `web` baru mulai setelah keduanya beres. Tanpa itu aplikasi bisa naik lebih dulu daripada skemanya lalu langsung mati, karena `NewDatabase` melakukan ping saat boot dan `Fatal` kalau gagal.

| Service | Peran |
|---|---|
| `postgres` | PostgreSQL 17, data di volume `postgres-data` |
| `redis` | Redis 7, persistence dimatikan — captcha memang state ber-TTL |
| `migrate` | Sekali jalan: migrasi ke `grand_erp`, lalu keluar |
| `migrate-test` | Sekali jalan: migrasi ke `grand_erp_test` untuk test |
| `seed` | Sekali jalan setelah `migrate`: memasang `db/seeder_postgres/` |
| `web` | Server HTTP di `:3000`, dengan healthcheck ke `/health` |
| `worker` | Worker latar; belum ada job, jadi memang hanya menunggu sinyal |

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

Yang **tidak** ditimpa compose — `app.name`, `captcha.ttl_seconds`, dan ketiga kunci `database.pool.*` — memakai nilai dari `config.example.json` yang terbangun ke dalam image. Kalau salah satunya perlu berbeda per lingkungan, tambahkan ke blok `environment:` service `web` dan `worker`.

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

```bash
createdb grand_erp

export DSN="postgres://postgres:PASSWORD@localhost:5432/grand_erp?sslmode=disable"
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
go run ./cmd/worker   # background worker (belum ada job)
```

Cek: `curl http://localhost:3000/health`, lalu buka <http://localhost:3000> untuk Swagger UI.

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

## Autentikasi

Seluruh `/api/v1` butuh bearer token, kecuali `POST /api/v1/auth/login`.

```bash
TOKEN=$(curl -s -X POST http://localhost:3000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin12345"}' | jq -r .data.token)

curl -s http://localhost:3000/api/v1/auth/me -H "Authorization: Bearer $TOKEN"
curl -s http://localhost:3000/api/v1/supplier -H "Authorization: Bearer $TOKEN"
```

**`JWT_SECRET` wajib diisi, minimal 32 karakter.** Server berhenti saat boot tanpa itu, dan `config.example.json` sengaja mengisinya dengan string kosong. Tidak ada default karena kunci default berarti kunci yang sama dipakai setiap deployment, dan siapa pun yang memegangnya bisa membuat token `SUPERADMIN` untuk user id mana pun. `docker compose` sudah memberi nilai dev supaya `up` langsung jalan; ganti lewat `.env`. Bangkitkan dengan `openssl rand -base64 48`.

> [!IMPORTANT]
> **Token tidak bisa dicabut.** Ini konsekuensi dari JWT stateless: tidak ada yang disimpan di server dan tidak ada lookup per request, jadi tidak ada apa pun yang bisa dibatalkan. Menonaktifkan user (`is_aktif: false`) atau mencabut rolenya **tidak** menyentuh token yang sudah keluar — aksesnya baru hilang saat token kedaluwarsa.
>
> Umur token karena itu adalah satu-satunya batas jendela tersebut. Defaultnya 60 menit (`JWT_TTL_MINUTES`), dan memperpanjangnya berarti memperpanjang jendela itu. Kalau pencabutan seketika dibutuhkan, sesi harus pindah ke Redis — Redis sudah terhubung tapi belum dipakai.

Role ikut di dalam token, jadi otorisasi tidak menyentuh database. Efek sampingnya: **role yang diberikan atau dicabut baru berlaku pada login berikutnya.** Hanya role `is_aktif` yang masuk token, jadi mempensiunkan sebuah role menghentikannya memberi izin pada login berikutnya meski penugasannya masih tercatat.

### Superadmin pertama

Karena `POST /api/v1/user` hanya untuk `SUPERADMIN`, tanpa user awal API terkunci dari dirinya sendiri. `db/seeder_postgres/004_superadmin.sql` memasangnya:

| Username | Password | Role |
|---|---|---|
| `admin` | `admin12345` | `SUPERADMIN` |

Passwordnya ada di repositori, jadi perlakukan sebagai kredensial sekali pakai. Setelah login pertama:

```bash
# ganti passwordnya
curl -X PATCH http://localhost:3000/api/v1/user/1 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"password":"password-yang-panjang-dan-acak"}'
```

Atau buat superadmin sungguhan lalu nonaktifkan yang bawaan dengan `{"is_aktif": false}`.

### Otorisasi

Membaca terbuka untuk siapa pun yang sudah login — operator yang tidak bisa melihat supplier tidak bisa bekerja, apa pun rolenya. Menulis dibagi menurut pemilik datanya. `SUPERADMIN` boleh apa saja.

| Resource | Baca | Tulis |
|---|---|---|
| `product`, `satuan`, `ruang`, `ekspedisi`, `supplier` | semua yang login | `SUPERADMIN`, `INVENTARIS` |
| `pelanggan` | semua yang login | `SUPERADMIN`, `CASHIER` |
| `role`, `user` | `SUPERADMIN` | `SUPERADMIN` |

`role` dan `user` tertutup termasuk untuk membaca: daftar akun beserta hak aksesnya sensitif, dan bisa menulis di sana adalah jalan eskalasi hak — beri diri sendiri `SUPERADMIN`, sisanya menyusul.

> [!NOTE]
> Pembagian di atas adalah **asumsi awal** yang ditarik dari tiga nama role, bukan hasil dari spesifikasi. Sesuaikan `setupAuthRoute` di `internal/delivery/http/route/route.go` begitu pembagian kerja sebenarnya jelas — seluruh kebijakannya ada di satu fungsi itu supaya bisa dibaca sekaligus.

Izin dihitung dari **gabungan** seluruh role: memegang salah satu role yang disyaratkan sudah cukup. Nama role dibandingkan tanpa memperhatikan huruf besar-kecil, karena `role.nama` unik tanpa memperhatikannya juga.

## Pengguna dan role

Satu user boleh punya banyak role, dan izinnya adalah **gabungan seluruh role** yang dipegang — tidak ada konsep "role yang sedang aktif". Role yang berlaku sekarang: `SUPERADMIN`, `CASHIER`, `INVENTARIS`.

Migrasi `000010` membetulkan bentuk lama, bukan sekadar menambah kolom:

- **`users.role_active` dibuang.** Migrasi `000002` memasang `UNIQUE (role_active)`, yang berlaku untuk seluruh tabel dan bukan per user — efek praktisnya satu sistem hanya bisa punya **satu kasir**, dan kasir kedua ditolak database. FK-nya juga menunjuk `user_role (id)` tanpa `user_id`, jadi role aktif user A bisa menunjuk penugasan milik user B. Kolomnya dihapus, tidak ditambal.
- Keunikan `username`, `email`, dan `role.nama` tidak peka huruf besar-kecil, lewat indeks `lower(...)` seperti kode master. `email` nullable, jadi banyak user tanpa email tetap boleh.
- `role` dapat `is_aktif` dan jejak perubahan; `user_role` dapat jejak kapan dan oleh siapa role diberikan.

Pemberian dan pencabutan role lewat `role_ids` pada `POST`/`PATCH /api/v1/user`, bukan sub-resource tersendiri — supaya baris user dan pemberian rolenya berada dalam satu transaksi. `role_ids` **mengganti seluruh himpunan**:

| Body | Artinya |
|---|---|
| tidak dikirim | role dibiarkan apa adanya |
| `[]` | cabut semua role |
| `[1, 3]` | user berakhir dengan tepat role 1 dan 3 |
| `null` | ditolak 400 — `[]` sudah berarti "tanpa role" |

Beberapa hal yang tidak terlihat dari daftar endpoint:

- **Password di-hash bcrypt** di lapisan usecase, tidak pernah disimpan, dicatat di log, atau dikembalikan. `UserResponse` sama sekali tidak punya field password, jadi kebocoran tidak mungkin secara struktural — bukan soal ingat atau tidak.
- **Role yang tetap dipegang tidak dicabut lalu diberikan ulang**, sehingga `user_role.created_at` tetap mencatat kapan pemberian itu benar-benar dimulai.
- Body yang **hanya** berisi `role_ids` tetap menggerakkan `updated_at` user — itu tetap perubahan pada user tersebut.
- Id role yang tidak ada **atau sudah dipensiunkan** ditolak 400. Foreign key tidak bisa membedakan role mati dari role hidup, jadi pengecekannya di usecase.
- Daftar role seorang user **termasuk role yang dipensiunkan setelah diberikan** — pemberiannya masih nyata dan masih perlu dicabut. `is_aktif` di dalam `roles` yang membedakannya.
- Filter `role_id` di endpoint list memakai `EXISTS`, bukan join: join akan mengembalikan satu baris per role dan melipatgandakan halaman untuk user yang punya beberapa role.
- **`user_role` satu-satunya tabel yang boleh `DELETE`.** Tabel jembatan ini tidak dirujuk tabel transaksi mana pun, jadi mencabut role tidak memutus foreign key dan tidak menghapus jejak dokumen — `created_by` di dokumen menunjuk `users`, bukan `user_role`.
- **Jangan ganti nama role yang sudah dipakai.** Nama role akan dipakai pengecekan izin begitu middleware otorisasi dibangun, dan tidak ada constraint database yang bisa mencegah nama diganti. Pensiunkan dengan `is_aktif: false` lalu buat role baru.

## Model data persediaan

Skema lengkap ada di migrasi `000002`–`000008`, **belum ada kode Go yang menyentuhnya**. Beberapa jaminan ditegakkan database, bukan aplikasi:

- **`kartu_stok` satu-satunya sumber kebenaran stok dan nilai persediaan.** Tidak ada kolom stok di tabel master, dan stok tidak pernah dihitung dengan menjumlahkan dokumen.
- **Append-only, dijaga trigger.** `UPDATE`, `DELETE`, dan `TRUNCATE` ditolak. Koreksi dilakukan lewat baris pembalik yang mengisi `id_kartu_stok_asal`.
- **Trigger yang menghitung saldo, bukan aplikasi.** `stok_awal`, `stok_akhir`, `harga_pokok_satuan`, `nilai_keluar`, dan `nilai_akhir` ditimpa saat insert. Aplikasi hanya mengirim arah pergerakan (`stok_masuk` **atau** `stok_keluar`, tidak keduanya), `nilai_masuk`, dan kolom referensi.
- **Rata-rata bergerak**: barang masuk menggeser harga pokok, barang keluar tidak pernah. Stok nol memaksa nilai persediaan tepat nol supaya sisa pembulatan tidak menumpuk.
- Saldo dipartisi per `(id_barang, id_ruang)` dan diurutkan pakai `id`, bukan tanggal. Insert mengambil `pg_advisory_xact_lock` pada pasangan itu.
- Trigger menolak stok negatif dan posting ke `periode` berstatus `TUTUP`. Bulan tanpa baris `periode` dihitung terbuka.
- Kuantitas di `kartu_stok` selalu dalam satuan dasar; `qty_input`/`id_satuan_input` hanya jejak audit apa yang diketik operator.
- Dokumen menyimpan snapshot (harga, faktor konversi, HPP); master menyimpan aturan berjalan. Retur menyalin harga pokok dari baris dokumen asal, bukan dari rata-rata berjalan.
- Stok hanya bergerak saat posting, tidak saat draft.
- Piutang dan utang saling cermin. `status_pembayaran` di `penjualan` dan `pembelian` adalah **cache**, selalu dihitung ulang dari alokasi dan retur berstatus POSTED. Giro belum `CAIR` bukan pembayaran. Kelebihan bayar itu normal dan mengendap jadi kredit.
- **Biaya angkut dialokasikan per koli** (`metode_alokasi_angkut` default `'KOLI'` sejak migrasi `000008`), dengan fallback ke `QTY` bila semua `jumlah_koli` nol.

Penjelasan lengkap, termasuk aturan yang masih harus divalidasi di aplikasi, ada di [CLAUDE.md](CLAUDE.md).

## API

Kontrak di [`docs/openapi.yaml`](docs/openapi.yaml). Setiap perubahan route, request, atau response wajib tercermin di sana pada commit yang sama.

**Swagger UI ada di root**, <http://localhost:3000>, membaca kontrak yang disajikan di `/openapi.yaml`. Spec-nya ditanam ke dalam biner lewat `go:embed` di [`docs/docs.go`](docs/docs.go), jadi server tidak perlu berkas `openapi.yaml` di sebelahnya — tapi berarti `docs/` ikut jadi input build, dan menghapusnya dari konteks build Docker menggagalkan kompilasi, bukan sekadar menghilangkan halaman dokumentasi.

Matikan dengan `web.swagger: false` di `config.json`, atau `WEB_SWAGGER=false`. Saat dimatikan kedua rute tidak terdaftar sama sekali dan menjawab 404 — bukan halaman kosong, dan bukan `/openapi.yaml` yang tetap terbuka. Defaultnya menyala, termasuk untuk `config.json` yang ditulis sebelum kunci ini ada.

> [!NOTE]
> Tombol **Try it out** memanggil `servers:` di dalam kontrak, yaitu `http://localhost:3000`. Kalau dokumentasi dibuka dari host atau port lain, sesuaikan daftar itu — spec disajikan apa adanya dan tidak ada yang menulis ulangnya saat dikirim.
>
> Halaman Swagger UI-nya sendiri memuat aset dari CDN unpkg, jadi butuh koneksi internet untuk tampil. API di belakangnya tidak.

| Method | Path | Keterangan |
|---|---|---|
| `GET` | `/` | Swagger UI (kalau `web.swagger` menyala) |
| `GET` | `/openapi.yaml` | Kontrak OpenAPI, ditanam di biner |
| `GET` | `/health` | Liveness probe |
| `POST` | `/api/v1/auth/login` | Tukar kredensial dengan token — satu-satunya `/api/v1` tanpa token |
| `GET` | `/api/v1/auth/me` | Sesi yang sedang berlaku menurut token |
| `GET` | `/api/v1/product` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/product` | Create, sekalian satuan konversinya |
| `GET` | `/api/v1/product/{id}` | Detail, dengan satuan dan riwayat harga jual |
| `PATCH` | `/api/v1/product/{id}` | Update `nama`, `stok_minimum`, `is_aktif` |
| `POST` | `/api/v1/product/{id}/satuan` | Tambah satuan konversi |
| `POST` | `/api/v1/product/{id}/harga-jual` | Buka versi harga jual baru |
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
| `GET` | `/api/v1/pelanggan` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/pelanggan` | Create |
| `GET` | `/api/v1/pelanggan/{id}` | Get by id |
| `PATCH` | `/api/v1/pelanggan/{id}` | Update parsial |
| `GET` | `/api/v1/ruang` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/ruang` | Create |
| `GET` | `/api/v1/ruang/{id}` | Get by id |
| `GET` | `/api/v1/role` | List — `page`, `size`, `search`, `is_aktif` |
| `POST` | `/api/v1/role` | Create |
| `GET` | `/api/v1/role/{id}` | Get by id |
| `PATCH` | `/api/v1/role/{id}` | Update parsial |
| `GET` | `/api/v1/user` | List — `page`, `size`, `search`, `is_aktif`, `role_id` |
| `POST` | `/api/v1/user` | Create, sekalian memberi role |
| `GET` | `/api/v1/user/{id}` | Get by id |
| `PATCH` | `/api/v1/user/{id}` | Update parsial, termasuk mengatur ulang role |

`ruang` belum punya PATCH. `page` default 1, `size` default 20 dengan batas 100. Tidak ada `DELETE` di seluruh master data — pakai `is_aktif = false`.

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
- Tujuh modul lengkap sampai OpenAPI: `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `role`, `user` (create/get/list/patch) dan `ruang` (create/get/list)
- User dengan banyak role, `role_ids` yang mengganti seluruh himpunan dalam satu transaksi, password ter-hash bcrypt
- Semantik PATCH dengan `model.Optional[T]`, keunikan kode tidak peka huruf, pemetaan pelanggaran unik jadi 409, escaping wildcard pencarian
- Autentikasi bearer JWT, otorisasi berbasis role per route, dan superadmin bawaan dari seeder
- Modul `product` beserta satuan konversi dan harga jual berversi, dengan satuan dasar otomatis dan periode harga yang tidak boleh tumpang tindih
- Wiring aplikasi penuh, graceful shutdown, penanganan error terpusat
- Test: unit test untuk `Optional`, validator, `EscapeLike`, dan amplop response; test usecase melawan PostgreSQL sungguhan

Belum ada:

- **Pengisian `created_by`/`updated_by`.** Sesi sudah membawa user id dan `middleware.SessionFrom` sudah mengeksposnya, tapi belum ada usecase yang memakainya — seluruh kolom pelaku masih ditulis `NULL`
- **Pencabutan sesi.** Token stateless tidak bisa dicabut sebelum kedaluwarsa
- **Logout dan refresh token**
- Captcha (Redis sudah terhubung tapi belum dipakai)
- Modul `periode`
- Lapisan Go untuk seluruh tabel persediaan dan transaksi (migrasi `000002`–`000008`)
- Validasi tingkat aplikasi yang tidak bisa ditegakkan database: satuan dasar `faktor = 1` di `product_satuan`, kuota retur kumulatif, `jumlah_koli` yang harus sama dengan `total_koli`, penjumlahan `alokasi_biaya`, penolakan edit dokumen POSTED, baris pembalik saat pembatalan, batas alokasi pembayaran, plafon kredit — didaftar lengkap di CLAUDE.md
- Job rekonsiliasi harian rantai saldo kartu stok
- Job apa pun di `cmd/worker`
