// Command web is the HTTP entrypoint for the ERP backend.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Arthafreestyle/ERP/internal/config"
)

func main() {
	viperConfig := config.NewViper()
	log := config.NewLogger(viperConfig)
	db := config.NewDatabase(viperConfig, log)
	redisClient := config.NewRedis(viperConfig, log)
	validate := config.NewValidator()
	app := config.NewFiber(viperConfig, log)

	config.Bootstrap(&config.BootstrapConfig{
		DB:       db,
		App:      app,
		Log:      log,
		Validate: validate,
		Config:   viperConfig,
		Redis:    redisClient,
	})

	address := fmt.Sprintf("%s:%d", viperConfig.GetString("web.host"), viperConfig.GetInt("web.port"))

	go func() {
		if err := app.Listen(address); err != nil {
			log.WithError(err).Fatal("web: server stopped")
		}
	}()

	log.WithField("address", address).Info("web: listening")

	// Drain in-flight requests before closing the pools.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("web: shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := app.ShutdownWithContext(ctx); err != nil {
		log.WithError(err).Error("web: graceful shutdown failed")
	}

	if err := db.Close(); err != nil {
		log.WithError(err).Error("web: closing database failed")
	}

	if err := redisClient.Close(); err != nil {
		log.WithError(err).Error("web: closing redis failed")
	}

	log.Info("web: stopped")
}
