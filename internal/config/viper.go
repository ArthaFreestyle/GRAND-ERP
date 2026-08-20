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

	// Access token lifetime is still the only bound on THAT token — nothing can
	// revoke it once issued, so a disabled user keeps API access until this
	// elapses. isu #24 shrinks it from an hour to 15 minutes and moves the real
	// session control to the refresh token instead, which lives in Redis and can
	// be revoked outright (logout, password change, is_aktif = false, every
	// grant revoked). 15 minutes is what an attacker gains from a stolen access
	// token even after the refresh token behind it is dead.
	//
	// jwt.secret has no default on purpose. A default signing key would be a key
	// every deployment shares, and anyone holding it can mint a SUPERADMIN token for
	// any user id — so NewAuthConfig refuses to start without one instead.
	cfg.SetDefault("jwt.ttl_minutes", 15)
	cfg.SetDefault("jwt.issuer", "grand-erp")

	// Refresh token lifetime: how long a session survives with no activity at
	// all before its holder has to log in again. 30 days is long enough that an
	// active user is never forced to re-authenticate just from the clock, while
	// still expiring an abandoned token eventually rather than relying solely on
	// explicit revocation.
	cfg.SetDefault("jwt.refresh_ttl_minutes", 43200)

	// Login throttling (isu #24 fase 4) replaces captcha: five failed attempts
	// from the same (ip, username) pair inside fifteen minutes and the pair is
	// refused, indistinguishably from an ordinary wrong password. See
	// "Autentikasi" in CLAUDE.md.
	cfg.SetDefault("throttle.login.max_attempts", 5)
	cfg.SetDefault("throttle.login.window_minutes", 15)

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

	// kartu_stok balance-chain reconciliation (isu #25). Daily, same as the
	// document sweep: while the table is small a full walk every day is the
	// honest answer, and there is no partial-scan mode yet to default instead.
	cfg.SetDefault("rekonsiliasi.interval", "24h")

	if err := cfg.ReadInConfig(); err != nil {
		panic(fmt.Errorf("config: cannot read config.json: %w", err))
	}

	return cfg
}
