package config

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// RekonsiliasiConfig carries the balance-chain reconciliation job's settings — isu
// #25. Worker-only: unlike DokumenConfig, nothing here is read by cmd/web, because
// the job itself only ever runs in cmd/worker.
type RekonsiliasiConfig struct {
	// Interval is the gap between sweeps. The issue is explicit that a partial scan
	// limited to recently-touched partitions is not built yet — a full walk is the
	// only honest answer while the table is small, and starting from a partial one
	// would mean old damage is never looked at again. See CLAUDE.md's isu #25 section.
	Interval time.Duration
}

// NewRekonsiliasiConfig reads and checks the setting, failing at startup rather
// than at the first tick — the same reasoning NewDokumenConfig gives for its own
// interval.
func NewRekonsiliasiConfig(cfg *viper.Viper, log *logrus.Logger) *RekonsiliasiConfig {
	interval := cfg.GetDuration("rekonsiliasi.interval")
	if interval <= 0 {
		log.WithField("rekonsiliasi.interval", cfg.GetString("rekonsiliasi.interval")).
			Fatal("rekonsiliasi.interval must be a positive duration, e.g. \"24h\"")
	}

	return &RekonsiliasiConfig{Interval: interval}
}
