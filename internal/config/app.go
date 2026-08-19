package config

import (
	"database/sql"

	deliveryhttp "Arthafreestyle/ERP/internal/delivery/http"
	"Arthafreestyle/ERP/internal/delivery/http/route"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type BootstrapConfig struct {
	DB       *sql.DB
	App      *fiber.App
	Log      *logrus.Logger
	Validate *validator.Validate
	Config   *viper.Viper
	Redis    *redis.Client
}

func Bootstrap(config *BootstrapConfig) {
	config.App.Use(recover.New())
	config.App.Use(requestid.New())
	config.App.Use(logger.New())

	ruangRepository := repository.NewRuangRepository()
	unitKerjaRepository := repository.NewUnitKerjaRepository()
	satuanRepository := repository.NewSatuanRepository()
	ekspedisiRepository := repository.NewEkspedisiRepository()
	supplierRepository := repository.NewSupplierRepository()
	pelangganRepository := repository.NewPelangganRepository()
	roleRepository := repository.NewRoleRepository()
	userRepository := repository.NewUserRepository()
	productRepository := repository.NewProductRepository()
	kartuStokRepository := repository.NewKartuStokRepository()
	counterRepository := repository.NewDocumentCounterRepository()
	pembelianRepository := repository.NewPembelianRepository()
	susulanRepository := repository.NewPenerimaanSusulanRepository()
	returRepository := repository.NewReturPembelianRepository()
	pembayaranRepository := repository.NewPembayaranUtangRepository()
	penerimaanRepository := repository.NewPenerimaanPembayaranRepository()
	mutasiRepository := repository.NewMutasiRepository()
	pemakaianRepository := repository.NewPemakaianRepository()
	penjualanRepository := repository.NewPenjualanRepository()
	dokumenRepository := repository.NewDokumenRepository()
	periodeRepository := repository.NewPeriodeRepository()
	stokOpnameRepository := repository.NewStokOpnameRepository()

	unitKerjaUseCase := usecase.NewUnitKerjaUseCase(
		config.DB, config.Log, config.Validate, unitKerjaRepository,
	)
	// RuangUseCase borrows UnitKerjaRepository to validate id_unit_kerja is an
	// active unit before writing (isu #12 fase 2) — the same reasoning
	// UserUseCase applies to role_ids via RoleRepository. It borrows
	// KartuStokRepository too, for exactly one read: whether a room being
	// retired still holds stock (isu #23 fase 3).
	ruangUseCase := usecase.NewRuangUseCase(
		config.DB, config.Log, config.Validate, ruangRepository, unitKerjaRepository, kartuStokRepository,
	)
	satuanUseCase := usecase.NewSatuanUseCase(
		config.DB, config.Log, config.Validate, satuanRepository,
	)
	ekspedisiUseCase := usecase.NewEkspedisiUseCase(
		config.DB, config.Log, config.Validate, ekspedisiRepository,
	)
	// SupplierUseCase borrows PembelianRepository for GET /supplier/{id}/utang, the same way
	// ProductUseCase does for riwayat-beli: the outstanding-payables query is SQL over
	// pembelian's tables, so it stays in that module's repository.
	supplierUseCase := usecase.NewSupplierUseCase(
		config.DB, config.Log, config.Validate, supplierRepository, pembelianRepository,
	)
	// PelangganUseCase borrows PenjualanRepository for GET /pelanggan/{id}/piutang
	// (isu #10 fase 2), the receivable-side mirror of SupplierUseCase borrowing
	// PembelianRepository above.
	pelangganUseCase := usecase.NewPelangganUseCase(
		config.DB, config.Log, config.Validate, pelangganRepository, penjualanRepository,
	)
	roleUseCase := usecase.NewRoleUseCase(
		config.DB, config.Log, config.Validate, roleRepository,
	)
	// ProductUseCase borrows three repositories for three reads: PembelianRepository
	// for GET /product/{id}/riwayat-beli, KartuStokRepository for
	// GET /product/{id}/stok (and, since isu #11, the balance batch behind
	// GET /pos/product), and RuangRepository to validate GET /pos/product's required
	// id_ruang names a real room before trusting it for anything. All three queries
	// are over other modules' tables, so they stay in those modules' repositories;
	// only the resource they answer for belongs to product.
	productUseCase := usecase.NewProductUseCase(
		config.DB, config.Log, config.Validate, productRepository, pembelianRepository,
		kartuStokRepository, ruangRepository,
	)
	// PeriodeUseCase is book closing. It is the module closest to master data — no
	// number, no lines, no posting — and it holds one repository, because closing a
	// month writes one row. What that row does is felt everywhere else: the kartu_stok
	// trigger reads it on every insert, so every module that writes stock inherits the
	// refusal without a line of code of its own.
	periodeUseCase := usecase.NewPeriodeUseCase(
		config.DB, config.Log, config.Validate, periodeRepository,
	)
	// PembelianUseCase writes the header, its lines, a reserved document number,
	// and one kartu_stok row per received line — all in one transaction, because a
	// partial posting would leave stock that no document explains and kartu_stok
	// cannot be edited afterwards.
	//
	// PeriodeRepository is only there for the error message: the kartu_stok
	// trigger is what actually refuses a closed month, but its RAISE carries
	// no constraint name, so without a read of its own this module could only tell an
	// operator that either the period is closed or the stock is short.
	//
	// RuangRepository is borrowed to validate Create's id_ruang against the
	// caller's active unit_kerja (isu #12 fase 5). StokOpnameRepository is the same
	// narrow borrow every kartu_stok writer gets, for periksaRuangBeku's message
	// (isu #15).
	pembelianUseCase := usecase.NewPembelianUseCase(
		config.DB, config.Log, config.Validate,
		pembelianRepository, productRepository, kartuStokRepository, counterRepository,
		periodeRepository, ruangRepository, stokOpnameRepository, unitKerjaRepository,
	)
	// PenerimaanSusulanUseCase reaches into pembelian on purpose: a follow-up
	// receipt is defined as the remainder of a purchase, so it reads that document's
	// lines for the outstanding quantity and the cost to copy, locks it to serialise
	// against other follow-ups, and rewrites its status_penerimaan cache — which the
	// purchase can no longer do for itself once it is POSTED.
	susulanUseCase := usecase.NewPenerimaanSusulanUseCase(
		config.DB, config.Log, config.Validate,
		susulanRepository, pembelianRepository, productRepository,
		kartuStokRepository, counterRepository, periodeRepository, stokOpnameRepository,
		ruangRepository, unitKerjaRepository,
	)
	// ReturPembelianUseCase takes the same repositories as the follow-up receipt
	// above, and for the same reasons — it is the mirror document. It reads the purchase's lines for
	// the cost to copy and locks the purchase to serialise returns against each other.
	// What it does NOT do is rewrite status_penerimaan: sending goods back does not make
	// a delivery incomplete.
	returUseCase := usecase.NewReturPembelianUseCase(
		config.DB, config.Log, config.Validate,
		returRepository, pembelianRepository, productRepository,
		kartuStokRepository, counterRepository, periodeRepository, stokOpnameRepository,
		ruangRepository, unitKerjaRepository,
	)
	// MutasiUseCase: a transfer points at no parent document, so nothing here reads
	// or rewrites another module's rows except for narrow reads: product for the
	// conversion factors, ruang to validate id_ruang_asal against the caller's
	// active unit_kerja (isu #12 fase 5) — never id_ruang_tujuan, since cross-unit
	// transfers are allowed by design — and, since isu #15, both rooms' ruang:
	// locks and periksaRuangBeku's message. kartu_stok is for two rows per line
	// plus the balance locks that keep two opposite transfers of the same goods from
	// deadlocking each other.
	mutasiUseCase := usecase.NewMutasiUseCase(
		config.DB, config.Log, config.Validate,
		mutasiRepository, productRepository, kartuStokRepository, counterRepository,
		periodeRepository, ruangRepository, stokOpnameRepository, unitKerjaRepository,
	)
	// PemakaianUseCase has no use for periksaRuangUnitAktif, unlike pembelian and
	// mutasi: isu #9 does not ask for id_ruang to be validated against the caller's
	// active unit_kerja. It reaches into product for the conversion factors and
	// kartu_stok for the outgoing rows plus the balance locks — the same ABBA risk
	// mutasi guards against, even though every line here shares one room. Its
	// RuangRepository is for LockShared alone (isu #15), and StokOpnameRepository is
	// the narrow borrow for periksaRuangBeku's message.
	pemakaianUseCase := usecase.NewPemakaianUseCase(
		config.DB, config.Log, config.Validate,
		pemakaianRepository, productRepository, kartuStokRepository, counterRepository,
		periodeRepository, ruangRepository, stokOpnameRepository, unitKerjaRepository,
	)
	// PenjualanUseCase's PelangganRepository is borrowed for exactly one narrow
	// read — a customer's plafon_kredit and running receivable, checked at Posting
	// for a KREDIT nota (isu #10 fase 2). Like pemakaian it has no use for
	// periksaRuangUnitAktif: isu #10 does not ask for id_ruang to be validated
	// against the caller's active unit_kerja. RuangRepository and
	// StokOpnameRepository are the same isu #15 additions pemakaian got, for
	// LockShared and periksaRuangBeku's message.
	penjualanUseCase := usecase.NewPenjualanUseCase(
		config.DB, config.Log, config.Validate,
		penjualanRepository, productRepository, pelangganRepository,
		kartuStokRepository, counterRepository, periodeRepository,
		ruangRepository, stokOpnameRepository, unitKerjaRepository,
	)
	// PembayaranUtangUseCase is the first transaction usecase that needs no
	// KartuStokRepository at all: money moving to a supplier changes no stock. It reaches
	// into pembelian to lock each invoice it allocates against, read what that invoice
	// still owes, and rewrite its status_pembayaran — which is a cache the purchase cannot
	// maintain for itself once it is POSTED.
	pembayaranUseCase := usecase.NewPembayaranUtangUseCase(
		config.DB, config.Log, config.Validate,
		pembayaranRepository, pembelianRepository, counterRepository, unitKerjaRepository,
	)
	// PenerimaanPembayaranUseCase is pembayaran-utang's mirror on the receivable
	// side (isu #20): it reaches into penjualan to lock each nota it allocates
	// against, read what that nota still has outstanding, and rewrite its
	// status_pembayaran — the same narrow borrow SupplierUseCase makes of
	// PembelianRepository, mirrored onto the receivable side.
	penerimaanUseCase := usecase.NewPenerimaanPembayaranUseCase(
		config.DB, config.Log, config.Validate,
		penerimaanRepository, penjualanRepository, counterRepository, unitKerjaRepository,
	)
	// StokOpnameUseCase is the seventh module to write kartu_stok, and the only one
	// whose Posting/Batal need RuangRepository for its exclusive/shared ruang: lock
	// rather than only the narrow read every other module borrows it for — isu #15.
	// It holds no ProductRepository: unlike every other document, a count is always
	// in the base unit, compared directly against kartu_stok, so there is no
	// conversion factor to resolve.
	stokOpnameUseCase := usecase.NewStokOpnameUseCase(
		config.DB, config.Log, config.Validate,
		stokOpnameRepository, kartuStokRepository, counterRepository,
		periodeRepository, ruangRepository, unitKerjaRepository,
	)
	// LaporanUseCase is isu #22 fase 3: three reports belonging to no single
	// module's resource, so it borrows KartuStokRepository (nilai persediaan,
	// pergerakan) and PenjualanRepository (laba kotor) the way ProductUseCase
	// borrows PembelianRepository for riwayat-beli.
	laporanUseCase := usecase.NewLaporanUseCase(
		config.DB, config.Log, config.Validate, kartuStokRepository, penjualanRepository,
	)
	// DokumenUseCase is the first usecase holding a store that is not the database.
	// The storage interface is what keeps that fact from spreading: swapping local
	// disk for S3 changes this line and nothing above it.
	//
	// Both constructors fail the process at boot rather than at the first upload —
	// an unwritable storage directory is a deployment mistake, and discovering it at
	// the receiving desk in the middle of a delivery is the wrong place.
	dokumenConfig := NewDokumenConfig(config.Config, config.Log)
	dokumenStorage, err := repository.NewLocalDokumenStorage(dokumenConfig.StoragePath)
	if err != nil {
		config.Log.WithError(err).Fatal("dokumen: storage tidak siap")
	}

	dokumenUseCase := usecase.NewDokumenUseCase(
		config.DB, config.Log, config.Validate, dokumenRepository, dokumenStorage,
		dokumenConfig.MaxUkuranByte, dokumenConfig.OrphanTTL,
	)
	// Fails the process at boot when jwt.secret is missing or too short, rather than
	// at the first login attempt.
	authConfig := NewAuthConfig(config.Config, config.Log)
	// throttleConfig is isu #24 fase 4 (rate limiting instead of captcha); it
	// Fatals the same way authConfig does on a non-positive setting, since a
	// zero max_attempts would lock out every caller immediately.
	throttleConfig := NewLoginThrottleConfig(config.Config, config.Log)
	// refreshTokenRepository and loginThrottleRepository are Redis-backed —
	// isu #24 is what finally gives the client wired since the very first
	// commit an actual job. Both are shared between AuthUseCase (refresh
	// tokens: issued, rotated, revoked) and UserUseCase (refresh tokens only:
	// revoked on password reset, deactivation, or every grant being pulled).
	refreshTokenRepository := repository.NewRefreshTokenRepository(config.Redis)
	loginThrottleRepository := repository.NewLoginThrottleRepository(config.Redis)
	authUseCase := usecase.NewAuthUseCase(
		config.DB, config.Log, config.Validate, userRepository,
		refreshTokenRepository, loginThrottleRepository,
		authConfig.Secret, authConfig.TTL, authConfig.RefreshTTL, authConfig.Issuer,
		throttleConfig.MaxAttempts, throttleConfig.Window,
	)
	// UserUseCase takes all three master-data repositories: granting a role at
	// a unit_kerja has to verify both the role and the unit exist and are
	// active, and that SQL belongs to each one's own repository (isu #12 fase
	// 3). RefreshTokenRepository is isu #24 fase 3: a password reset,
	// deactivation, or full grant revocation through PATCH /user/{id} all end
	// that user's live sessions the same way AuthUseCase.ChangePassword does.
	userUseCase := usecase.NewUserUseCase(
		config.DB, config.Log, config.Validate, userRepository, roleRepository, unitKerjaRepository,
		refreshTokenRepository,
	)

	ruangController := deliveryhttp.NewRuangController(config.Log, ruangUseCase)
	unitKerjaController := deliveryhttp.NewUnitKerjaController(config.Log, unitKerjaUseCase)
	satuanController := deliveryhttp.NewSatuanController(config.Log, satuanUseCase)
	ekspedisiController := deliveryhttp.NewEkspedisiController(config.Log, ekspedisiUseCase)
	supplierController := deliveryhttp.NewSupplierController(config.Log, supplierUseCase)
	pelangganController := deliveryhttp.NewPelangganController(config.Log, pelangganUseCase)
	authController := deliveryhttp.NewAuthController(config.Log, authUseCase)
	dokumenController := deliveryhttp.NewDokumenController(config.Log, dokumenUseCase)
	periodeController := deliveryhttp.NewPeriodeController(config.Log, periodeUseCase)
	productController := deliveryhttp.NewProductController(config.Log, productUseCase)
	pembelianController := deliveryhttp.NewPembelianController(config.Log, pembelianUseCase)
	susulanController := deliveryhttp.NewPenerimaanSusulanController(config.Log, susulanUseCase)
	returController := deliveryhttp.NewReturPembelianController(config.Log, returUseCase)
	mutasiController := deliveryhttp.NewMutasiController(config.Log, mutasiUseCase)
	pemakaianController := deliveryhttp.NewPemakaianController(config.Log, pemakaianUseCase)
	penjualanController := deliveryhttp.NewPenjualanController(config.Log, penjualanUseCase)
	pembayaranController := deliveryhttp.NewPembayaranUtangController(config.Log, pembayaranUseCase)
	penerimaanController := deliveryhttp.NewPenerimaanPembayaranController(config.Log, penerimaanUseCase)
	stokOpnameController := deliveryhttp.NewStokOpnameController(config.Log, stokOpnameUseCase)
	laporanController := deliveryhttp.NewLaporanController(config.Log, laporanUseCase)
	roleController := deliveryhttp.NewRoleController(config.Log, roleUseCase)
	userController := deliveryhttp.NewUserController(config.Log, userUseCase)

	// Left nil when web.swagger is false, which is what keeps the docs routes
	// unregistered. NewViper defaults the key to true, so a config.json predating
	// the key still serves the docs rather than silently losing them.
	var docsController *deliveryhttp.DocsController
	if config.Config.GetBool("web.swagger") {
		docsController = deliveryhttp.NewDocsController(config.Log)
	}

	routeConfig := route.RouteConfig{
		App:                  config.App,
		AuthUseCase:          authUseCase,
		DocsController:       docsController,
		AuthController:       authController,
		DokumenController:    dokumenController,
		PeriodeController:    periodeController,
		PembelianController:  pembelianController,
		SusulanController:    susulanController,
		ReturController:      returController,
		MutasiController:     mutasiController,
		PemakaianController:  pemakaianController,
		PenjualanController:  penjualanController,
		PembayaranController: pembayaranController,
		PenerimaanController: penerimaanController,
		StokOpnameController: stokOpnameController,
		ProductController:    productController,
		LaporanController:    laporanController,
		UnitKerjaController:  unitKerjaController,
		RuangController:      ruangController,
		SatuanController:     satuanController,
		EkspedisiController:  ekspedisiController,
		SupplierController:   supplierController,
		PelangganController:  pelangganController,
		RoleController:       roleController,
		UserController:       userController,
	}
	routeConfig.Setup()
}
