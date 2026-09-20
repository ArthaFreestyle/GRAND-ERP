package usecase_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/config"
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	// Registers the "pgx" driver with database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// These tests exercise the usecase layer against a real PostgreSQL, because most
// of what the issue asks to prove lives in the database rather than in Go:
// pagination stability under duplicate names, ILIKE wildcard escaping, several
// rows sharing kode = NULL under a unique index, and NUMERIC surviving a
// round-trip. A mock would happily agree with a wrong query.
//
// Point them at a scratch database:
//
//	TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/grand_erp_test?sslmode=disable' go test ./internal/usecase/...
//
// Without that variable the whole package skips, so `go test ./...` stays green
// on a machine with no database.
const dsnEnv = "TEST_DATABASE_URL"

// redisAddrEnv gates Redis the same way dsnEnv gates PostgreSQL. AuthUseCase
// now hard-requires Redis for Login itself (isu #24 — refresh tokens and
// login throttling), which is not a new category of dependency: the running
// app already refuses to boot without Redis (config.NewRedis Fatals on a
// failed ping), this just extends that same requirement into the test
// harness. Point it at the Redis docker-compose.yml already brings up on
// 6379 — a DIFFERENT db index than a developer's own default (0) is not
// required, since newApp only ever touches its own refresh:*/login_throttle:*
// keys and cleans them up itself; see flushAuthKeys.
const redisAddrEnv = "TEST_REDIS_ADDR"

var testDB *sql.DB
var testRedis *redis.Client

func TestMain(m *testing.M) {
	dsn := os.Getenv(dsnEnv)
	if dsn != "" {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "open %s: %v\n", dsnEnv, err)
			os.Exit(1)
		}

		if err := db.Ping(); err != nil {
			fmt.Fprintf(os.Stderr, "ping %s: %v\n", dsnEnv, err)
			os.Exit(1)
		}

		testDB = db
	}

	if addr := os.Getenv(redisAddrEnv); addr != "" {
		client := redis.NewClient(&redis.Options{Addr: addr})

		if err := client.Ping(context.Background()).Err(); err != nil {
			fmt.Fprintf(os.Stderr, "ping %s: %v\n", redisAddrEnv, err)
			os.Exit(1)
		}

		testRedis = client
	}

	// Nothing to connect to; every test skips itself via requireDB/requireRedis.
	code := m.Run()

	if testDB != nil {
		_ = testDB.Close()
	}
	if testRedis != nil {
		_ = testRedis.Close()
	}

	os.Exit(code)
}

// app carries the usecases under test.
type app struct {
	satuan       *usecase.SatuanUseCase
	ekspedisi    *usecase.EkspedisiUseCase
	supplier     *usecase.SupplierUseCase
	pelanggan    *usecase.PelangganUseCase
	ruang        *usecase.RuangUseCase
	unitKerja    *usecase.UnitKerjaUseCase
	role         *usecase.RoleUseCase
	product      *usecase.ProductUseCase
	user         *usecase.UserUseCase
	pembelian    *usecase.PembelianUseCase
	susulan      *usecase.PenerimaanSusulanUseCase
	retur        *usecase.ReturPembelianUseCase
	mutasi       *usecase.MutasiUseCase
	pemakaian    *usecase.PemakaianUseCase
	saldoAwal    *usecase.SaldoAwalUseCase
	penjualan    *usecase.PenjualanUseCase
	pembayaran   *usecase.PembayaranUtangUseCase
	penerimaan   *usecase.PenerimaanPembayaranUseCase
	stokOpname   *usecase.StokOpnameUseCase
	laporan      *usecase.LaporanUseCase
	dokumen      *usecase.DokumenUseCase
	rekonsiliasi *usecase.RekonsiliasiUseCase
	periode      *usecase.PeriodeUseCase
	presensi     *usecase.PresensiUseCase
	auth         *usecase.AuthUseCase
	// ocr's FakturReader starts as a fakeFakturReader returning no lines at all —
	// every real ocr_pembelian_test.go case swaps app.ocr.FakturReader for its own
	// fake before calling, the exported-field shape isu #39 relies on instead of a
	// constructor argument per test.
	ocr *usecase.OCRPembelianUseCase
	// dokumenDir is where this test's attachments land, so a test can check that a
	// file really was written — or really was removed — rather than trusting the row.
	dokumenDir string
}

// Attachment limits used by the tests. Deliberately not the production defaults:
// the size limit has to be small enough that a test can exceed it without allocating
// ten megabytes, and what is being proved is that the limit holds, not what it is
// set to.
const (
	testMaxUkuranDokumen = 64 * 1024
	testOrphanTTL        = 24 * time.Hour

	// testAuthSecret only needs to satisfy NewAuthConfig's 32-character
	// minimum; nothing signed with it is ever meant to verify against a real
	// deployment's key.
	testAuthSecret     = "test-only-secret-not-for-prod-32"
	testAuthTTL        = time.Hour
	testAuthRefreshTTL = time.Hour
	testAuthIssuer     = "grand-erp-test"

	// Generous on purpose: these tests log in far more often than any real
	// attacker's pacing, and a tight limit would make ordinary test traffic
	// trip the same throttle the rate-limiting tests exist to exercise
	// deliberately.
	testMaxLoginAttempts    = 1000
	testLoginThrottleWindow = time.Minute
)

// newApp wires the same graph config.Bootstrap does, minus Fiber, and empties the
// master tables so each test starts from a known state.
func newApp(t *testing.T) *app {
	t.Helper()
	requireDB(t)
	requireRedis(t)
	truncateMaster(t)
	flushAuthKeys(t)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // keep expected-error tests quiet
	validate := config.NewValidator()

	roleRepository := repository.NewRoleRepository()
	unitKerjaRepository := repository.NewUnitKerjaRepository()
	userRepository := repository.NewUserRepository()
	ruangRepository := repository.NewRuangRepository()
	productRepository := repository.NewProductRepository()
	pembelianRepository := repository.NewPembelianRepository()
	penjualanRepository := repository.NewPenjualanRepository()
	kartuStokRepository := repository.NewKartuStokRepository()
	counterRepository := repository.NewDocumentCounterRepository()
	periodeRepository := repository.NewPeriodeRepository()
	stokOpnameRepository := repository.NewStokOpnameRepository()

	// t.TempDir is removed when the test ends, so attachments never land in the
	// developer's real dokumen.storage_path and one test cannot see another's files.
	dokumenDir := t.TempDir()

	dokumenStorage, err := repository.NewLocalDokumenStorage(dokumenDir)
	if err != nil {
		t.Fatalf("dokumen storage: %v", err)
	}

	return &app{
		dokumenDir: dokumenDir,
		dokumen: usecase.NewDokumenUseCase(
			testDB, log, validate, repository.NewDokumenRepository(), dokumenStorage,
			testMaxUkuranDokumen, testOrphanTTL,
		),
		rekonsiliasi: usecase.NewRekonsiliasiUseCase(
			testDB, log, kartuStokRepository,
		),
		satuan: usecase.NewSatuanUseCase(
			testDB, log, validate, repository.NewSatuanRepository(),
		),
		ekspedisi: usecase.NewEkspedisiUseCase(
			testDB, log, validate, repository.NewEkspedisiRepository(),
		),
		supplier: usecase.NewSupplierUseCase(
			testDB, log, validate, repository.NewSupplierRepository(), pembelianRepository,
		),
		pelanggan: usecase.NewPelangganUseCase(
			testDB, log, validate, repository.NewPelangganRepository(), penjualanRepository,
		),
		ruang: usecase.NewRuangUseCase(
			testDB, log, validate, ruangRepository, unitKerjaRepository, kartuStokRepository,
		),
		unitKerja: usecase.NewUnitKerjaUseCase(
			testDB, log, validate, unitKerjaRepository,
		),
		role: usecase.NewRoleUseCase(
			testDB, log, validate, roleRepository,
		),
		product: usecase.NewProductUseCase(
			testDB, log, validate, productRepository, pembelianRepository,
			kartuStokRepository, ruangRepository, unitKerjaRepository,
		),
		user: usecase.NewUserUseCase(
			testDB, log, validate, userRepository, roleRepository, unitKerjaRepository,
			repository.NewRefreshTokenRepository(testRedis),
		),
		periode: usecase.NewPeriodeUseCase(
			testDB, log, validate, periodeRepository,
		),
		presensi: usecase.NewPresensiUseCase(
			testDB, log, validate, repository.NewPresensiRepository(),
		),
		auth: usecase.NewAuthUseCase(
			testDB, log, validate, userRepository,
			repository.NewRefreshTokenRepository(testRedis), repository.NewLoginThrottleRepository(testRedis),
			testAuthSecret, testAuthTTL, testAuthRefreshTTL, testAuthIssuer,
			testMaxLoginAttempts, testLoginThrottleWindow,
		),
		pembelian: usecase.NewPembelianUseCase(
			testDB, log, validate,
			pembelianRepository, productRepository,
			kartuStokRepository, counterRepository, periodeRepository, ruangRepository,
			stokOpnameRepository, unitKerjaRepository,
		),
		susulan: usecase.NewPenerimaanSusulanUseCase(
			testDB, log, validate,
			repository.NewPenerimaanSusulanRepository(), pembelianRepository,
			productRepository, kartuStokRepository, counterRepository, periodeRepository,
			stokOpnameRepository, ruangRepository, unitKerjaRepository,
		),
		retur: usecase.NewReturPembelianUseCase(
			testDB, log, validate,
			repository.NewReturPembelianRepository(), pembelianRepository,
			productRepository, kartuStokRepository, counterRepository, periodeRepository,
			stokOpnameRepository, ruangRepository, unitKerjaRepository,
		),
		mutasi: usecase.NewMutasiUseCase(
			testDB, log, validate,
			repository.NewMutasiRepository(), productRepository,
			kartuStokRepository, counterRepository, periodeRepository, ruangRepository,
			stokOpnameRepository, unitKerjaRepository,
		),
		pemakaian: usecase.NewPemakaianUseCase(
			testDB, log, validate,
			repository.NewPemakaianRepository(), productRepository,
			kartuStokRepository, counterRepository, periodeRepository,
			ruangRepository, stokOpnameRepository, unitKerjaRepository,
		),
		saldoAwal: usecase.NewSaldoAwalUseCase(
			testDB, log, validate,
			repository.NewSaldoAwalRepository(), productRepository,
			kartuStokRepository, counterRepository, periodeRepository,
			ruangRepository, stokOpnameRepository, unitKerjaRepository,
		),
		penjualan: usecase.NewPenjualanUseCase(
			testDB, log, validate,
			penjualanRepository, productRepository, repository.NewPelangganRepository(),
			kartuStokRepository, counterRepository, periodeRepository,
			ruangRepository, stokOpnameRepository, unitKerjaRepository,
		),
		stokOpname: usecase.NewStokOpnameUseCase(
			testDB, log, validate,
			stokOpnameRepository, kartuStokRepository, counterRepository,
			periodeRepository, ruangRepository, unitKerjaRepository, productRepository,
		),
		pembayaran: usecase.NewPembayaranUtangUseCase(
			testDB, log, validate,
			repository.NewPembayaranUtangRepository(), pembelianRepository, counterRepository,
			unitKerjaRepository,
		),
		penerimaan: usecase.NewPenerimaanPembayaranUseCase(
			testDB, log, validate,
			repository.NewPenerimaanPembayaranRepository(), penjualanRepository, counterRepository,
			unitKerjaRepository,
		),
		laporan: usecase.NewLaporanUseCase(
			testDB, log, validate, kartuStokRepository, penjualanRepository,
		),
		ocr: usecase.NewOCRPembelianUseCase(
			log, testDB, validate, productRepository, pembelianRepository, ruangRepository,
			&fakeFakturReader{}, testMaxUkuranDokumen, time.Second,
		),
	}
}

// fakeFakturReader is the FakturReader every OCR test hands in — isu #39's own
// decision that no usecase test may call Gemini for real. Baca answers whatever
// Hasil and Err are set to at the moment it is called, which is enough for every
// test in ocr_pembelian_test.go: each one sets these on app.ocr.FakturReader (a
// *fakeFakturReader, asserted back from the interface field) before calling the
// usecase.
type fakeFakturReader struct {
	Hasil *repository.FakturReaderHasil
	Err   error
}

func (f *fakeFakturReader) Baca(
	context.Context, []byte, string, repository.JenisOCRFaktur, []entity.Product,
) (*repository.FakturReaderHasil, error) {
	if f.Err != nil {
		return nil, f.Err
	}

	return f.Hasil, nil
}

func requireDB(t *testing.T) {
	t.Helper()

	if testDB == nil {
		t.Skipf("%s is not set; skipping database-backed test", dsnEnv)
	}
}

func requireRedis(t *testing.T) {
	t.Helper()

	if testRedis == nil {
		t.Skipf("%s is not set; skipping Redis-backed test", redisAddrEnv)
	}
}

// flushAuthKeys is truncateMaster's Redis counterpart: every key isu #24
// writes lives under one of these two prefixes, so a targeted SCAN+DEL clears
// exactly what a previous test left behind without touching anything else
// that might live on the same Redis instance — deliberately not FLUSHDB,
// which would be safe against the scratch database this is meant to run
// against but not against a developer accidentally pointing TEST_REDIS_ADDR
// at a real one.
func flushAuthKeys(t *testing.T) {
	t.Helper()

	for _, pattern := range []string{"refresh:*", "login_throttle:*"} {
		iter := testRedis.Scan(ctx(), 0, pattern, 0).Iterator()

		var keys []string
		for iter.Next(ctx()) {
			keys = append(keys, iter.Val())
		}

		if err := iter.Err(); err != nil {
			t.Fatalf("scan %s: %v", pattern, err)
		}

		if len(keys) == 0 {
			continue
		}

		if err := testRedis.Del(ctx(), keys...).Err(); err != nil {
			t.Fatalf("flush %s: %v", pattern, err)
		}
	}
}

// truncateMaster clears the tables with DELETE rather than TRUNCATE: TRUNCATE would
// have to cascade into kartu_stok, whose guard trigger raises on TRUNCATE by design.
//
// Order matters. user_role comes before users and role because it references both;
// users comes after the master tables because every one of them has a created_by
// pointing at it. kartu_stok goes first of all — it references product, ruang,
// satuan, and users, and pembelian_detail rows are what its postings describe.
// penerimaan_susulan_detail and retur_pembelian_detail both point at pembelian_detail,
// so both come before it.
//
// kartu_stok also refuses DELETE, by the same append-only trigger, so the trigger is
// switched off for the length of the wipe. That is a licence a test database gets
// and production never does: it is exactly the guarantee the whole valuation rests
// on, and disabling it anywhere else would defeat the point of having it.
func truncateMaster(t *testing.T) {
	t.Helper()

	if _, err := testDB.Exec(`ALTER TABLE kartu_stok DISABLE TRIGGER kartu_stok_append_only`); err != nil {
		t.Fatalf("disable kartu_stok guard: %v", err)
	}
	defer func() {
		if _, err := testDB.Exec(`ALTER TABLE kartu_stok ENABLE TRIGGER kartu_stok_append_only`); err != nil {
			t.Fatalf("re-enable kartu_stok guard: %v", err)
		}
	}()

	for _, table := range []string{
		// stok_opname_detail points AT kartu_stok (id_kartu_stok_cutoff,
		// id_kartu_stok_penyesuaian), the reverse of every other table below it —
		// so it has to go before kartu_stok is cleared, not after, or its rows are
		// left pointing at deleted kartu_stok rows and the DELETE on kartu_stok
		// itself fails on the foreign key. stok_opname_detail before stok_opname for
		// the ordinary child-before-parent reason.
		"stok_opname_detail", "stok_opname",
		// saldo_awal_detail.id_kartu_stok points AT kartu_stok too (isu #43) — the same
		// reversed side stok_opname_detail sits on, so it goes here with it, before
		// kartu_stok, or the wipe fails on the foreign key.
		"saldo_awal_detail", "saldo_awal",
		// Children before parents. kartu_stok references product, ruang, satuan and
		// users; penerimaan_susulan_detail references pembelian_detail, so it has to
		// go before it.
		"kartu_stok",
		"penerimaan_susulan_detail", "penerimaan_susulan",
		"retur_pembelian_detail", "retur_pembelian",
		// mutasi points at no document, only at product, ruang, satuan, and users, so it
		// only has to precede those four. It goes here rather than later because a
		// leftover row would keep a ruang or a product from being deleted below.
		"mutasi_detail", "mutasi",
		// pemakaian points at no document either, only at product, ruang, satuan, and
		// users (id_pemohon, disetujui_oleh, created_by, dibatalkan_oleh) — same shape as
		// mutasi, so it sits right beside it for the same reason.
		"pemakaian_detail", "pemakaian",
		// pembayaran_utang_alokasi references pembelian, and pembayaran_utang references
		// supplier and users, so both come before pembelian.
		"pembayaran_utang_alokasi", "pembayaran_utang",
		"pembelian_detail", "pembelian", "document_counter",
		// dokumen references users through created_by, and its ref_table/ref_id pair
		// is polymorphic — no foreign key — so nothing else constrains where it sits.
		"dokumen",
		// periode references users twice, through ditutup_oleh and dibuka_oleh, and
		// nothing references periode — the kartu_stok trigger reads it but no column
		// points at it. So it only has to precede users. Clearing it between tests
		// matters more than most: a row left behind here does not fail a later test's
		// insert, it silently refuses its posting.
		"periode",
		// presensi references users (id_user, dikoreksi_oleh) and unit_kerja
		// (id_unit_kerja), and nothing references presensi — it writes no kartu_stok
		// and points at no document. So it only has to precede those two, and it sits
		// alongside dokumen and periode for the same reason they do.
		"presensi",
		// pembayaran_alokasi references penjualan, and penerimaan_pembayaran
		// references pelanggan and users — isu #20's mirror of
		// pembayaran_utang_alokasi/pembayaran_utang sitting before pembelian, so
		// both have to clear before penjualan does.
		"pembayaran_alokasi", "penerimaan_pembayaran",
		// penjualan_detail.id_harga_jual references product_harga_jual, and
		// penjualan_detail.id_product/penjualan.id_ruang/penjualan.id_pelanggan
		// reference product/ruang/pelanggan, so penjualan has to precede all three
		// (and users, through created_by/dibatalkan_oleh). isu #10 gave it a full Go
		// layer; before that, isu #8 fase 2's tests inserted these two tables with
		// raw SQL to prove the already-used-by-a-document guard, and the schema has
		// existed since migration 000006 regardless.
		"penjualan_detail", "penjualan",
		// product_harga_jual and product_satuan reference product; product
		// references satuan and users, so it has to go before both.
		// product_unit_kerja is a join row pointing at product, unit_kerja and users
		// (created_by) and nothing points at it, so it goes before all three.
		"product_unit_kerja", "product_harga_jual", "product_satuan", "product",
		"supplier", "pelanggan", "ekspedisi", "satuan",
		// ruang.id_unit_kerja references unit_kerja, so ruang has to go first.
		// user_role.id_unit_kerja references unit_kerja too (isu #12 fase 3), so
		// user_role now has to precede unit_kerja as well as users and role —
		// unit_kerja.created_by references users, so it has to go before that.
		"ruang", "user_role", "role", "unit_kerja", "users",
	} {
		if _, err := testDB.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func ctx() context.Context { return context.Background() }

// masukKatalog puts a product into the catalog of each given unit_kerja.
//
// A product created BEFORE a unit exists is not in that unit's catalog — a new unit
// starts empty, which is the point of a per-unit catalog — and pembelianFixture creates
// its product after its own unit but before any extra unit a test adds. A test that adds
// a unit and then documents into one of its rooms needs this. Raw SQL rather than
// ProductUseCase.SetUnitKerja, which replaces the whole set and so would need the
// existing memberships restated; ON CONFLICT DO NOTHING keeps a repeat harmless.
func masukKatalog(t *testing.T, idProduct int64, idUnitKerja ...int64) {
	t.Helper()

	for _, unit := range idUnitKerja {
		if _, err := testDB.Exec(
			`INSERT INTO product_unit_kerja (id_product, id_unit_kerja) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			idProduct, unit,
		); err != nil {
			t.Fatalf("masukkan produk %d ke katalog unit %d: %v", idProduct, unit, err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// testActorSeq gives every testActor call a unique username without a
// database round trip to check for one first.
var testActorSeq int64

// testActor seeds a real users row directly via SQL, bypassing UserUseCase —
// the same way db/seeder_postgres/004_superadmin.sql bootstraps the very
// first user in production, because creating one through the usecase needs
// an actor id that does not exist yet. isu #23 fase 2 made every master-data
// Create/Update require a real ActorID; this is what every fixture that needs
// one calls, once, and then reuses.
func testActor(t *testing.T) int64 {
	t.Helper()

	n := atomic.AddInt64(&testActorSeq, 1)

	var id int64
	err := testDB.QueryRowContext(ctx(), `
		INSERT INTO users (username, password, is_aktif)
		VALUES ($1, 'x', true)
		RETURNING id`,
		fmt.Sprintf("test_actor_%d_%d", time.Now().UnixNano(), n),
	).Scan(&id)
	if err != nil {
		t.Fatalf("seed test actor: %v", err)
	}

	return id
}
