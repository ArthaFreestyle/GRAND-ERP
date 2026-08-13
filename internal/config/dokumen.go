package config

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// DokumenConfig carries the attachment settings, validated once at boot.
//
// Every one of these is read by both entrypoints, not just the web server: the
// worker needs StoragePath to delete files and OrphanTTL to decide which, and it
// reads them from the same keys — which is why the compose file sets them on both
// services. A worker configured with a different storage path than the web server
// deletes nothing and reports success while doing it.
type DokumenConfig struct {
	StoragePath string
	// MaxUkuranByte is derived from dokumen.max_size_mb and is what the upload stream
	// is cut off at.
	MaxUkuranByte int64
	// OrphanTTL is how long an unattached upload is left alone. It is generous on
	// purpose: a form filled in at the end of a shift and saved the next morning must
	// still find its photos.
	OrphanTTL time.Duration
	// CleanupInterval is the gap between sweeps in the worker.
	CleanupInterval time.Duration
}

// NewDokumenConfig reads and checks the dokumen settings, failing at startup rather
// than at the first upload of the day.
//
// Every key has a default in NewViper, so a config.json written before this feature
// existed still boots. What is checked here is that the values make sense: a
// non-positive size limit accepts nothing at all, and a non-positive interval is a
// ticker that panics.
func NewDokumenConfig(cfg *viper.Viper, log *logrus.Logger) *DokumenConfig {
	path := cfg.GetString("dokumen.storage_path")
	if path == "" {
		log.Fatal("dokumen.storage_path is not set; set it in config.json or DOKUMEN_STORAGE_PATH")
	}

	maxMB := cfg.GetInt64("dokumen.max_size_mb")
	if maxMB <= 0 {
		log.WithField("dokumen.max_size_mb", maxMB).
			Fatal("dokumen.max_size_mb must be greater than zero")
	}

	ttl := time.Duration(cfg.GetInt64("dokumen.orphan_ttl_hours")) * time.Hour
	if ttl <= 0 {
		log.WithField("dokumen.orphan_ttl_hours", cfg.GetInt64("dokumen.orphan_ttl_hours")).
			Fatal("dokumen.orphan_ttl_hours must be greater than zero")
	}

	interval := cfg.GetDuration("dokumen.cleanup_interval")
	if interval <= 0 {
		log.WithField("dokumen.cleanup_interval", cfg.GetString("dokumen.cleanup_interval")).
			Fatal("dokumen.cleanup_interval must be a positive duration, e.g. \"24h\"")
	}

	return &DokumenConfig{
		StoragePath:     path,
		MaxUkuranByte:   maxMB * 1024 * 1024,
		OrphanTTL:       ttl,
		CleanupInterval: interval,
	}
}
