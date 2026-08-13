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

	// Attachment settings, defaulted for the same reason web.swagger is: a
	// config.json written before this feature existed must still boot, and an absent
	// key would otherwise read as zero — a size limit of 0 MB accepts nothing and an
	// interval of 0 panics the ticker.
	//
	// 10 MB because a photo from a current phone clears 5 MB without trying. 24 hours
	// before an unattached upload is considered abandoned, which covers a form left
	// open overnight, and one sweep a day to collect them: a file nobody claimed costs
	// disk and nothing else, so there is no hurry.
	cfg.SetDefault("dokumen.storage_path", "./data/dokumen")
	cfg.SetDefault("dokumen.max_size_mb", 10)
	cfg.SetDefault("dokumen.orphan_ttl_hours", 24)
	cfg.SetDefault("dokumen.cleanup_interval", "24h")

	if err := cfg.ReadInConfig(); err != nil {
		panic(fmt.Errorf("config: cannot read config.json: %w", err))
	}

	return cfg
}
