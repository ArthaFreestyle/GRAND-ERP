// Command worker is the background job entrypoint. It shares the same config
// and repository layers as cmd/web; only the trigger differs (schedule or
// queue instead of HTTP). No jobs are registered yet.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"Arthafreestyle/ERP/internal/config"
)

func main() {
	viperConfig := config.NewViper()
	log := config.NewLogger(viperConfig)
	db := config.NewDatabase(viperConfig, log)
	redisClient := config.NewRedis(viperConfig, log)

	defer func() {
		if err := db.Close(); err != nil {
			log.WithError(err).Error("worker: closing database failed")
		}
		if err := redisClient.Close(); err != nil {
			log.WithError(err).Error("worker: closing redis failed")
		}
	}()

	log.Info("worker: started (no jobs registered)")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("worker: stopped")
}
