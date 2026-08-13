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
	dokumenRepository := repository.NewDokumenRepository()

	ruangUseCase := usecase.NewRuangUseCase(
		config.DB, config.Log, config.Validate, ruangRepository,
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
	pelangganUseCase := usecase.NewPelangganUseCase(
		config.DB, config.Log, config.Validate, pelangganRepository,
	)
	roleUseCase := usecase.NewRoleUseCase(
		config.DB, config.Log, config.Validate, roleRepository,
	)
	// ProductUseCase borrows PembelianRepository for GET /product/{id}/riwayat-beli.
	// The purchase-history query is SQL over pembelian tables, so it stays in that
	// module's repository; only the resource it answers for belongs to product.
	productUseCase := usecase.NewProductUseCase(
		config.DB, config.Log, config.Validate, productRepository, pembelianRepository,
	)
	// PembelianUseCase takes four repositories: posting a purchase writes the
	// header, its lines, a reserved document number, and one kartu_stok row per
	// received line — all in one transaction, because a partial posting would leave
	// stock that no document explains and kartu_stok cannot be edited afterwards.
	pembelianUseCase := usecase.NewPembelianUseCase(
		config.DB, config.Log, config.Validate,
		pembelianRepository, productRepository, kartuStokRepository, counterRepository,
	)
	// PenerimaanSusulanUseCase reaches into pembelian on purpose: a follow-up
	// receipt is defined as the remainder of a purchase, so it reads that document's
	// lines for the outstanding quantity and the cost to copy, locks it to serialise
	// against other follow-ups, and rewrites its status_penerimaan cache — which the
	// purchase can no longer do for itself once it is POSTED.
	susulanUseCase := usecase.NewPenerimaanSusulanUseCase(
		config.DB, config.Log, config.Validate,
		susulanRepository, pembelianRepository, productRepository,
		kartuStokRepository, counterRepository,
	)
	// ReturPembelianUseCase takes the same five as the follow-up receipt above, and for
	// the same reasons — it is the mirror document. It reads the purchase's lines for
	// the cost to copy and locks the purchase to serialise returns against each other.
	// What it does NOT do is rewrite status_penerimaan: sending goods back does not make
	// a delivery incomplete.
	returUseCase := usecase.NewReturPembelianUseCase(
		config.DB, config.Log, config.Validate,
		returRepository, pembelianRepository, productRepository,
		kartuStokRepository, counterRepository,
	)
	// PembayaranUtangUseCase is the first transaction usecase that needs no
	// KartuStokRepository at all: money moving to a supplier changes no stock. It reaches
	// into pembelian to lock each invoice it allocates against, read what that invoice
	// still owes, and rewrite its status_pembayaran — which is a cache the purchase cannot
	// maintain for itself once it is POSTED.
	pembayaranUseCase := usecase.NewPembayaranUtangUseCase(
		config.DB, config.Log, config.Validate,
		pembayaranRepository, pembelianRepository, counterRepository,
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
	authUseCase := usecase.NewAuthUseCase(
		config.DB, config.Log, config.Validate, userRepository,
		authConfig.Secret, authConfig.TTL, authConfig.Issuer,
	)
	// UserUseCase takes both repositories: granting a role has to verify the role
	// exists and is active, and that SQL belongs to role's repository.
	userUseCase := usecase.NewUserUseCase(
		config.DB, config.Log, config.Validate, userRepository, roleRepository,
	)

	ruangController := deliveryhttp.NewRuangController(config.Log, ruangUseCase)
	satuanController := deliveryhttp.NewSatuanController(config.Log, satuanUseCase)
	ekspedisiController := deliveryhttp.NewEkspedisiController(config.Log, ekspedisiUseCase)
	supplierController := deliveryhttp.NewSupplierController(config.Log, supplierUseCase)
	pelangganController := deliveryhttp.NewPelangganController(config.Log, pelangganUseCase)
	authController := deliveryhttp.NewAuthController(config.Log, authUseCase)
	dokumenController := deliveryhttp.NewDokumenController(config.Log, dokumenUseCase)
	productController := deliveryhttp.NewProductController(config.Log, productUseCase)
	pembelianController := deliveryhttp.NewPembelianController(config.Log, pembelianUseCase)
	susulanController := deliveryhttp.NewPenerimaanSusulanController(config.Log, susulanUseCase)
	returController := deliveryhttp.NewReturPembelianController(config.Log, returUseCase)
	pembayaranController := deliveryhttp.NewPembayaranUtangController(config.Log, pembayaranUseCase)
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
		PembelianController:  pembelianController,
		SusulanController:    susulanController,
		ReturController:      returController,
		PembayaranController: pembayaranController,
		ProductController:    productController,
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
