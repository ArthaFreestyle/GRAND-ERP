package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// NewViper loads config.json from the working directory (or a parent, so tests
// run from a package directory still find it). Any key can be overridden by an
// environment variable: database.host -> DATABASE_HOST.
func NewViper() *viper.Viper {
	cfg := viper.New()
	cfg.SetConfigName("config")
	cfg.SetConfigType("json")
	cfg.AddConfigPath(".")
	cfg.AddConfigPath("./..")
	cfg.AddConfigPath("./../..")
	cfg.AddConfigPath("./../../..")

	cfg.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	cfg.AutomaticEnv()

	if err := cfg.ReadInConfig(); err != nil {
		panic(fmt.Errorf("config: cannot read config.json: %w", err))
	}

	return cfg
}
