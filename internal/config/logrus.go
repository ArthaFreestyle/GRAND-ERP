package config

import (
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NewLogger builds the single application logger. log.level follows logrus
// levels: 0=panic .. 4=warn, 5=debug, 6=trace.
func NewLogger(cfg *viper.Viper) *logrus.Logger {
	log := logrus.New()
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.Level(cfg.GetUint32("log.level")))
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
	})

	return log
}
