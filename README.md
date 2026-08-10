# GRAND-ERP

Backend ERP dengan fokus pada persediaan, pembelian, dan penjualan. Ditulis dengan Go + Fiber v3 di atas PostgreSQL, tanpa ORM.

> **Status: master data dan pengguna berjalan, transaksi belum.** Tujuh modul sudah punya kode Go lengkap dari migrasi sampai OpenAPI — `satuan`, `ekspedisi`, `supplier`, `pelanggan`, `ruang`, `role`, dan `user`. Skema persediaan/pembelian/penjualan sudah termigrasi tetapi belum punya satu pun lapisan Go. Lihat [Status & Roadmap](#status--roadmap).

> [!WARNING]
> **`/api/v1/user` belum punya autentikasi maupun otorisasi.** Selama middleware belum ada, siapa pun yang bisa menjangkau server bisa membuat user dan memberinya `SUPERADMIN`. Jangan paparkan ke jaringan yang tidak dipercaya.

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

Isi `database.password` di `config.json`. File ini masuk `.gitignore` dan tidak pernah ikut ter-commit — karena itu **setiap kunci config baru wajib ditambahkan juga ke `config.example.json`**, kalau tidak clone baru kehilangan kunci itu tanpa suara.

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
```

`003_role.sql` memasang tiga role yang berlaku sekarang — `SUPERADMIN`, `CASHIER`, `INVENTARIS`. Tanpa itu `user_role` tidak bisa diisi dan setiap user berakhir tanpa role.

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
- Wiring aplikasi penuh, graceful shutdown, penanganan error terpusat
- Test: unit test untuk `Optional`, validator, `EscapeLike`, dan amplop response; test usecase melawan PostgreSQL sungguhan

Belum ada:

- **Autentikasi dan sesi.** `users` sudah ada dan menyimpan hash bcrypt, tapi belum ada yang memverifikasinya — jadi belum ada pelaku, dan setiap `created_by`/`updated_by` masih ditulis `NULL`
- **Middleware otorisasi.** Nama role sudah ada, tapi belum ada yang memeriksanya; seluruh endpoint terbuka
- Captcha (Redis sudah terhubung tapi belum dipakai)
- Modul `product` dan `periode`
- Lapisan Go untuk seluruh tabel persediaan dan transaksi (migrasi `000002`–`000008`)
- Validasi tingkat aplikasi yang tidak bisa ditegakkan database: satuan dasar `faktor = 1` di `product_satuan`, kuota retur kumulatif, `jumlah_koli` yang harus sama dengan `total_koli`, penjumlahan `alokasi_biaya`, penolakan edit dokumen POSTED, baris pembalik saat pembatalan, batas alokasi pembayaran, plafon kredit — didaftar lengkap di CLAUDE.md
- Job rekonsiliasi harian rantai saldo kartu stok
- Job apa pun di `cmd/worker`
