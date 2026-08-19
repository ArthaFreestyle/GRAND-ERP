package config

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// minSecretLength is 32 bytes because the tokens are HS256, whose security rests
// entirely on the key being unguessable. A short shared secret is brute-forceable
// offline from a single captured token, and the reward is the ability to mint a
// SUPERADMIN token for any user id.
const minSecretLength = 32

// AuthConfig carries the token settings, validated once at boot.
type AuthConfig struct {
	Secret     string
	TTL        time.Duration
	RefreshTTL time.Duration
	Issuer     string
}

// NewAuthConfig reads and checks the JWT settings, failing at startup rather than at
// the first login.
//
// It refuses to run with a missing or short jwt.secret. There is deliberately no
// default and no generated-at-startup fallback: a baked-in default is a key shared by
// every deployment, and a random key per process would silently invalidate every
// token on restart and break outright across more than one instance. Both fail
// quietly, which is worse than not starting.
func NewAuthConfig(cfg *viper.Viper, log *logrus.Logger) *AuthConfig {
	secret := cfg.GetString("jwt.secret")

	if secret == "" {
		log.Fatal("jwt.secret is not set; set it in config.json or JWT_SECRET " +
			"(at least 32 characters, e.g. `openssl rand -base64 48`)")
	}

	if len(secret) < minSecretLength {
		log.WithFields(logrus.Fields{
			"length":  len(secret),
			"minimum": minSecretLength,
		}).Fatal("jwt.secret is too short to sign HS256 tokens safely")
	}

	ttl := time.Duration(cfg.GetInt("jwt.ttl_minutes")) * time.Minute
	if ttl <= 0 {
		log.WithField("jwt.ttl_minutes", cfg.GetInt("jwt.ttl_minutes")).
			Fatal("jwt.ttl_minutes must be greater than zero")
	}

	// The refresh token, not the access token, is now what actually bounds a
	// session — isu #24. It lives in Redis and can be revoked outright
	// (logout, password change, is_aktif = false, every grant revoked); TTL
	// here only bounds how long an untouched one survives.
	refreshTTL := time.Duration(cfg.GetInt("jwt.refresh_ttl_minutes")) * time.Minute
	if refreshTTL <= 0 {
		log.WithField("jwt.refresh_ttl_minutes", cfg.GetInt("jwt.refresh_ttl_minutes")).
			Fatal("jwt.refresh_ttl_minutes must be greater than zero")
	}

	return &AuthConfig{
		Secret:     secret,
		TTL:        ttl,
		RefreshTTL: refreshTTL,
		Issuer:     cfg.GetString("jwt.issuer"),
	}
}
