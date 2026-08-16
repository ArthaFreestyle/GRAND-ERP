package usecase_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/config"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	// Registers the "pgx" driver with database/sql.
	_ "github.com/jackc/pgx/v5/stdlib"
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

var testDB *sql.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		// Nothing to connect to; every test skips itself via requireDB.
		os.Exit(m.Run())
	}

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

	code := m.Run()

	_ = db.Close()

	os.Exit(code)
}

// app carries the usecases under test.
type app struct {
	satuan     *usecase.SatuanUseCase
	ekspedisi  *usecase.EkspedisiUseCase
	supplier   *usecase.SupplierUseCase
	pelanggan  *usecase.PelangganUseCase
	ruang      *usecase.RuangUseCase
	unitKerja  *usecase.UnitKerjaUseCase
	role       *usecase.RoleUseCase
	product    *usecase.ProductUseCase
	user       *usecase.UserUseCase
	pembelian  *usecase.PembelianUseCase
	susulan    *usecase.PenerimaanSusulanUseCase
	retur      *usecase.ReturPembelianUseCase
	mutasi     *usecase.MutasiUseCase
	pemakaian  *usecase.PemakaianUseCase
	penjualan  *usecase.PenjualanUseCase
	pembayaran *usecase.PembayaranUtangUseCase
	dokumen    *usecase.DokumenUseCase
	periode    *usecase.PeriodeUseCase
	auth       *usecase.AuthUseCase
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
	testAuthSecret = "test-only-secret-not-for-prod-32"
	testAuthTTL    = time.Hour
	testAuthIssuer = "grand-erp-test"
)

// newApp wires the same graph config.Bootstrap does, minus Fiber, and empties the
// master tables so each test starts from a known state.
func newApp(t *testing.T) *app {
	t.Helper()
	requireDB(t)
	truncateMaster(t)

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
			testDB, log, validate, ruangRepository, unitKerjaRepository,
		),
		unitKerja: usecase.NewUnitKerjaUseCase(
			testDB, log, validate, unitKerjaRepository,
		),
		role: usecase.NewRoleUseCase(
			testDB, log, validate, roleRepository,
		),
		product: usecase.NewProductUseCase(
			testDB, log, validate, productRepository, pembelianRepository,
			kartuStokRepository,
		),
		user: usecase.NewUserUseCase(
			testDB, log, validate, userRepository, roleRepository, unitKerjaRepository,
		),
		periode: usecase.NewPeriodeUseCase(
			testDB, log, validate, periodeRepository,
		),
		auth: usecase.NewAuthUseCase(
			testDB, log, validate, userRepository, testAuthSecret, testAuthTTL, testAuthIssuer,
		),
		pembelian: usecase.NewPembelianUseCase(
			testDB, log, validate,
			pembelianRepository, productRepository,
			kartuStokRepository, counterRepository, periodeRepository, ruangRepository,
		),
		susulan: usecase.NewPenerimaanSusulanUseCase(
			testDB, log, validate,
			repository.NewPenerimaanSusulanRepository(), pembelianRepository,
			productRepository, kartuStokRepository, counterRepository, periodeRepository,
		),
		retur: usecase.NewReturPembelianUseCase(
			testDB, log, validate,
			repository.NewReturPembelianRepository(), pembelianRepository,
			productRepository, kartuStokRepository, counterRepository, periodeRepository,
		),
		mutasi: usecase.NewMutasiUseCase(
			testDB, log, validate,
			repository.NewMutasiRepository(), productRepository,
			kartuStokRepository, counterRepository, periodeRepository, ruangRepository,
		),
		pemakaian: usecase.NewPemakaianUseCase(
			testDB, log, validate,
			repository.NewPemakaianRepository(), productRepository,
			kartuStokRepository, counterRepository, periodeRepository,
		),
		penjualan: usecase.NewPenjualanUseCase(
			testDB, log, validate,
			penjualanRepository, productRepository, repository.NewPelangganRepository(),
			kartuStokRepository, counterRepository, periodeRepository,
		),
		pembayaran: usecase.NewPembayaranUtangUseCase(
			testDB, log, validate,
			repository.NewPembayaranUtangRepository(), pembelianRepository, counterRepository,
		),
	}
}

func requireDB(t *testing.T) {
	t.Helper()

	if testDB == nil {
		t.Skipf("%s is not set; skipping database-backed test", dsnEnv)
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
		"product_harga_jual", "product_satuan", "product",
		"supplier", "pelanggan", "ekspedisi", "satuan",
		// ruang.id_unit_kerja references unit_kerja, so ruang has to go first.
		// user_role.id_unit_kerja references unit_kerja too (isu #12 fase 3), so
		// user_role now has to precede unit_kerja as well as users and role —
		// unit_kerja.created_by references users, so it has to go before that.
		"ruang", "user_role", "unit_kerja", "users", "role",
	} {
		if _, err := testDB.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func ctx() context.Context { return context.Background() }

func ptr[T any](v T) *T { return &v }
