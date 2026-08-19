package config

import (
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// LoginThrottleConfig bounds how many failed logins one (ip, username) pair
// may make inside one window before AuthUseCase.Login starts refusing further
// attempts on that exact pair — isu #24 fase 4, chosen over captcha. See
// "Autentikasi" in CLAUDE.md for why the rejection it produces is
// indistinguishable from an ordinary wrong-password failure.
type LoginThrottleConfig struct {
	MaxAttempts int
	Window      time.Duration
}

// NewLoginThrottleConfig reads throttle.login.*, both defaulted in NewViper so
// an absent key still boots with a sane limit rather than silently disabling
// throttling (0 attempts) or locking every caller out forever (a 0 window
// never expires the counter).
func NewLoginThrottleConfig(cfg *viper.Viper, log *logrus.Logger) *LoginThrottleConfig {
	maxAttempts := cfg.GetInt("throttle.login.max_attempts")
	if maxAttempts <= 0 {
		log.WithField("throttle.login.max_attempts", maxAttempts).
			Fatal("throttle.login.max_attempts must be greater than zero")
	}

	windowMinutes := cfg.GetInt("throttle.login.window_minutes")
	if windowMinutes <= 0 {
		log.WithField("throttle.login.window_minutes", windowMinutes).
			Fatal("throttle.login.window_minutes must be greater than zero")
	}

	return &LoginThrottleConfig{
		MaxAttempts: maxAttempts,
		Window:      time.Duration(windowMinutes) * time.Minute,
	}
}
