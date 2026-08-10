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

	// GetBool answers false for a key that is absent, so without this default a
	// config.json written before web.swagger existed would silently lose the docs
	// page. Defaulting to true keeps "docs are on unless turned off", and
	// WEB_SWAGGER=false still overrides it.
	cfg.SetDefault("web.swagger", true)

	// Token lifetime is the only bound on a stateless session: nothing can revoke a
	// JWT once issued, so a disabled user keeps access until this elapses. Kept to an
	// hour for that reason, and deliberately not days.
	//
	// jwt.secret has no default on purpose. A default signing key would be a key
	// every deployment shares, and anyone holding it can mint a SUPERADMIN token for
	// any user id — so NewAuthConfig refuses to start without one instead.
	cfg.SetDefault("jwt.ttl_minutes", 60)
	cfg.SetDefault("jwt.issuer", "grand-erp")

	if err := cfg.ReadInConfig(); err != nil {
		panic(fmt.Errorf("config: cannot read config.json: %w", err))
	}

	return cfg
}
