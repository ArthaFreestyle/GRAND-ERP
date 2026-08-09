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

// BootstrapConfig carries the process-wide dependencies built in main.
type BootstrapConfig struct {
	DB       *sql.DB
	App      *fiber.App
	Log      *logrus.Logger
	Validate *validator.Validate
	Config   *viper.Viper
	Redis    *redis.Client
}

// Bootstrap is the composition root: repositories -> usecases -> controllers ->
// routes. Every wiring decision lives here, so no package constructs its own
// dependencies.
func Bootstrap(config *BootstrapConfig) {
	config.App.Use(recover.New())
	config.App.Use(requestid.New())
	config.App.Use(logger.New())

	ruangRepository := repository.NewRuangRepository()

	ruangUseCase := usecase.NewRuangUseCase(
		config.DB, config.Log, config.Validate, ruangRepository,
	)

	ruangController := deliveryhttp.NewRuangController(config.Log, ruangUseCase)

	routeConfig := route.RouteConfig{
		App:             config.App,
		RuangController: ruangController,
	}
	routeConfig.Setup()
}
