// Command worker is the background job entrypoint. It shares the same config,
// repository, and usecase layers as cmd/web; only the trigger differs — a schedule
// instead of an HTTP request.
//
// Two jobs are registered: sweeping up uploaded files that were never attached to
// a document, and reconciling the kartu_stok balance chain (isu #25) — reporting a
// discrepancy, never repairing one. See internal/config/worker.go.
package main

import (
	"context"
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

	scheduler := config.BootstrapWorker(&config.WorkerBootstrapConfig{
		DB:       db,
		Log:      log,
		Validate: config.NewValidator(),
		Config:   viperConfig,
	})

	// Cancelled on the first signal, which is what stops the jobs. Run blocks until
	// every one of them has returned, so a sweep in progress finishes its current
	// file rather than being cut off between deleting one and marking its row.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("worker: started")

	scheduler.Run(ctx)

	log.Info("worker: stopped")
}
