package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

// GeminiConfig carries the OCR feature's settings (isu #39).
//
// Unlike jwt.secret, an empty APIKey is not a boot failure — see NewGeminiConfig.
type GeminiConfig struct {
	APIKey  string
	Model   string
	Timeout time.Duration
}

// NewGeminiConfig reads the gemini.* settings without ever failing the process.
//
// gemini.api_key has no default, the same reasoning jwt.secret follows: a key
// committed to config.example.json is a key every deployment would share. But where
// a missing jwt.secret means the application cannot function at all, a missing
// gemini.api_key means one convenience is unavailable — config.Bootstrap reads
// APIKey == "" and leaves the OCR routes unregistered, the same nil-controller shape
// web.swagger already uses for the docs routes. Fatal-ing here would make the whole
// server refuse to boot over a feature nobody has to use.
func NewGeminiConfig(cfg *viper.Viper) *GeminiConfig {
	return &GeminiConfig{
		APIKey:  strings.TrimSpace(cfg.GetString("gemini.api_key")),
		Model:   cfg.GetString("gemini.model"),
		Timeout: time.Duration(cfg.GetInt64("gemini.timeout_seconds")) * time.Second,
	}
}
