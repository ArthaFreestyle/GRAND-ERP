package config

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// PresensiConfig carries the daily sweep job's settings — isu #40 fase 5.
// Worker-only: cmd/web never runs this job and so never reads it, the same
// split RekonsiliasiConfig already has.
type PresensiConfig struct {
	// SapuanInterval is the gap between sweeps. A row left BUKA past its own
	// date is only ever the result of someone who never tapped Pulang again —
	// resigned, on long leave, or sick for a week — so a daily cadence is
	// enough; nothing about this job benefits from running more often.
	SapuanInterval time.Duration
}

// NewPresensiConfig reads and checks the setting, failing at startup rather
// than at the first tick — the same reasoning NewRekonsiliasiConfig gives for
// its own interval.
func NewPresensiConfig(cfg *viper.Viper, log *logrus.Logger) *PresensiConfig {
	interval := cfg.GetDuration("presensi.sapuan_interval")
	if interval <= 0 {
		log.WithField("presensi.sapuan_interval", cfg.GetString("presensi.sapuan_interval")).
			Fatal("presensi.sapuan_interval must be a positive duration, e.g. \"24h\"")
	}

	return &PresensiConfig{SapuanInterval: interval}
}
