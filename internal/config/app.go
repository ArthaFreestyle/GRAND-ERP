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

	ruangUseCase := usecase.NewRuangUseCase(
		config.DB, config.Log, config.Validate, ruangRepository,
	)
	satuanUseCase := usecase.NewSatuanUseCase(
		config.DB, config.Log, config.Validate, satuanRepository,
	)
	ekspedisiUseCase := usecase.NewEkspedisiUseCase(
		config.DB, config.Log, config.Validate, ekspedisiRepository,
	)
	supplierUseCase := usecase.NewSupplierUseCase(
		config.DB, config.Log, config.Validate, supplierRepository,
	)
	pelangganUseCase := usecase.NewPelangganUseCase(
		config.DB, config.Log, config.Validate, pelangganRepository,
	)
	roleUseCase := usecase.NewRoleUseCase(
		config.DB, config.Log, config.Validate, roleRepository,
	)
	productUseCase := usecase.NewProductUseCase(
		config.DB, config.Log, config.Validate, productRepository,
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
	productController := deliveryhttp.NewProductController(config.Log, productUseCase)
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
		App:                 config.App,
		AuthUseCase:         authUseCase,
		DocsController:      docsController,
		AuthController:      authController,
		ProductController:   productController,
		RuangController:     ruangController,
		SatuanController:    satuanController,
		EkspedisiController: ekspedisiController,
		SupplierController:  supplierController,
		PelangganController: pelangganController,
		RoleController:      roleController,
		UserController:      userController,
	}
	routeConfig.Setup()
}
