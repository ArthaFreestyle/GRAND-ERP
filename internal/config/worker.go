package config

import (
	"context"
	"database/sql"
	"time"

	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Job is one piece of scheduled work: a name for the log, how often it runs, and
// what it does.
//
// Jalankan returns the number of things it handled so a quiet run can be logged
// differently from a busy one — a cleanup that suddenly deletes thousands of files
// is worth noticing, and a count is the cheapest way to notice it.
type Job struct {
	Nama     string
	Interval time.Duration
	Jalankan func(ctx context.Context) (int, error)
}

// Scheduler runs jobs on a fixed interval. It is a time.Ticker per job and nothing
// more.
//
// A cron library was the alternative and was not taken. One daily job needs a gap
// between runs, not a schedule specification — nothing here has to happen at 03:00
// on weekdays — and a ticker costs no dependency and no format to learn. Move to
// robfig/cron when a second job appears whose timing genuinely differs; until then
// the library would be answering a question nobody has asked.
//
// The consequence to know: a ticker measures from process start, so restarting the
// worker restarts the clock. That is fine for a job whose work is "delete what has
// been sitting for a day" and would not be for one that must run at a wall-clock
// time.
type Scheduler struct {
	Log  *logrus.Logger
	Jobs []Job
}

// Run executes every job once and then on its interval, returning when ctx is
// cancelled.
//
// Running once at startup is deliberate. Without it, a worker restarted daily —
// which is what a deployment or a crash loop looks like — would never reach the
// first tick of a daily job, and the cleanup would quietly never happen.
func (s *Scheduler) Run(ctx context.Context) {
	if len(s.Jobs) == 0 {
		s.Log.Warn("worker: tidak ada job terdaftar")

		return
	}

	selesai := make(chan struct{}, len(s.Jobs))

	for _, job := range s.Jobs {
		go func(job Job) {
			defer func() { selesai <- struct{}{} }()

			s.jalankan(ctx, job)

			ticker := time.NewTicker(job.Interval)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.jalankan(ctx, job)
				}
			}
		}(job)
	}

	for range s.Jobs {
		<-selesai
	}
}

// jalankan runs one job and logs the outcome. An error is logged rather than
// returned: a failed sweep must not stop the schedule, because the next run is the
// retry.
func (s *Scheduler) jalankan(ctx context.Context, job Job) {
	mulai := time.Now()

	jumlah, err := job.Jalankan(ctx)
	if err != nil {
		s.Log.WithError(err).WithField("job", job.Nama).Error("worker: job gagal")

		return
	}

	s.Log.WithFields(logrus.Fields{
		"job":      job.Nama,
		"jumlah":   jumlah,
		"durasi":   time.Since(mulai).String(),
		"interval": job.Interval.String(),
	}).Info("worker: job selesai")
}

// WorkerBootstrapConfig is the worker's half of BootstrapConfig. It has no Fiber app
// and no Redis: nothing scheduled so far serves HTTP or keeps ephemeral state.
type WorkerBootstrapConfig struct {
	DB       *sql.DB
	Log      *logrus.Logger
	Validate *validator.Validate
	Config   *viper.Viper
}

// BootstrapWorker wires the worker's dependency graph and returns the scheduler to
// run.
//
// It is the counterpart of Bootstrap and exists for the same reason: every
// dependency is built in this package and injected downward, so no other package
// constructs its own. The two share the repository and usecase layers exactly —
// the only difference between the entrypoints is what triggers the work.
func BootstrapWorker(config *WorkerBootstrapConfig) *Scheduler {
	dokumenConfig := NewDokumenConfig(config.Config, config.Log)

	// The worker must resolve dokumen.storage_path to the same directory the web
	// server writes to, which in Docker means the one volume mounted on both
	// services. Pointed anywhere else, the cleanup finds rows, fails to find their
	// files, and — because Hapus treats a missing file as already done — would happily
	// mark them deleted while the real files pile up untouched.
	dokumenStorage, err := repository.NewLocalDokumenStorage(dokumenConfig.StoragePath)
	if err != nil {
		config.Log.WithError(err).Fatal("dokumen: storage tidak siap")
	}

	dokumenUseCase := usecase.NewDokumenUseCase(
		config.DB, config.Log, config.Validate,
		repository.NewDokumenRepository(), dokumenStorage,
		dokumenConfig.MaxUkuranByte, dokumenConfig.OrphanTTL,
	)

	rekonsiliasiConfig := NewRekonsiliasiConfig(config.Config, config.Log)
	rekonsiliasiUseCase := usecase.NewRekonsiliasiUseCase(
		config.DB, config.Log, repository.NewKartuStokRepository(),
	)

	return &Scheduler{
		Log: config.Log,
		Jobs: []Job{
			{
				Nama:     "pembersihan-dokumen-yatim",
				Interval: dokumenConfig.CleanupInterval,
				Jalankan: dokumenUseCase.BersihkanYatim,
			},
			// isu #25: the second job cmd/worker has ever had, still on the same
			// time.Ticker Scheduler already provides — daily, same as the sweep
			// above, so nothing about its timing needs robfig/cron either.
			{
				Nama:     "rekonsiliasi-rantai-kartu-stok",
				Interval: rekonsiliasiConfig.Interval,
				Jalankan: rekonsiliasiUseCase.PeriksaRantaiSaldo,
			},
		},
	}
}
