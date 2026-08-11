package usecase_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

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
	satuan    *usecase.SatuanUseCase
	ekspedisi *usecase.EkspedisiUseCase
	supplier  *usecase.SupplierUseCase
	pelanggan *usecase.PelangganUseCase
	ruang     *usecase.RuangUseCase
	role      *usecase.RoleUseCase
	product   *usecase.ProductUseCase
	user      *usecase.UserUseCase
	pembelian *usecase.PembelianUseCase
	susulan   *usecase.PenerimaanSusulanUseCase
}

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
	productRepository := repository.NewProductRepository()
	pembelianRepository := repository.NewPembelianRepository()
	kartuStokRepository := repository.NewKartuStokRepository()
	counterRepository := repository.NewDocumentCounterRepository()

	return &app{
		satuan: usecase.NewSatuanUseCase(
			testDB, log, validate, repository.NewSatuanRepository(),
		),
		ekspedisi: usecase.NewEkspedisiUseCase(
			testDB, log, validate, repository.NewEkspedisiRepository(),
		),
		supplier: usecase.NewSupplierUseCase(
			testDB, log, validate, repository.NewSupplierRepository(),
		),
		pelanggan: usecase.NewPelangganUseCase(
			testDB, log, validate, repository.NewPelangganRepository(),
		),
		ruang: usecase.NewRuangUseCase(
			testDB, log, validate, repository.NewRuangRepository(),
		),
		role: usecase.NewRoleUseCase(
			testDB, log, validate, roleRepository,
		),
		product: usecase.NewProductUseCase(
			testDB, log, validate, productRepository, pembelianRepository,
		),
		user: usecase.NewUserUseCase(
			testDB, log, validate, repository.NewUserRepository(), roleRepository,
		),
		pembelian: usecase.NewPembelianUseCase(
			testDB, log, validate,
			pembelianRepository, productRepository,
			kartuStokRepository, counterRepository,
		),
		susulan: usecase.NewPenerimaanSusulanUseCase(
			testDB, log, validate,
			repository.NewPenerimaanSusulanRepository(), pembelianRepository,
			productRepository, kartuStokRepository, counterRepository,
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
		"pembelian_detail", "pembelian", "document_counter",
		// product_harga_jual and product_satuan reference product; product
		// references satuan and users, so it has to go before both.
		"product_harga_jual", "product_satuan", "product",
		"supplier", "pelanggan", "ekspedisi", "satuan", "ruang",
		"user_role", "users", "role",
	} {
		if _, err := testDB.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func ctx() context.Context { return context.Background() }

func ptr[T any](v T) *T { return &v }
