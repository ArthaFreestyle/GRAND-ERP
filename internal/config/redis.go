package config

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NewRedis returns the Redis client used for TTL-scoped state such as captcha
// sessions. Nothing durable belongs here.
func NewRedis(cfg *viper.Viper, log *logrus.Logger) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.GetString("redis.host"), cfg.GetInt("redis.port")),
		Password: cfg.GetString("redis.password"),
		DB:       cfg.GetInt("redis.db"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		log.WithError(err).Fatal("redis: ping failed")
	}

	log.Info("redis connected")

	return client
}
