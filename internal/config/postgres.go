package config

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	// Registers the "pgx" driver with database/sql. There is no ORM in this
	// project; repositories talk to *sql.DB directly.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NewDatabase opens the PostgreSQL pool and verifies it with a ping, so a bad
// DSN fails at boot instead of on the first request.
func NewDatabase(cfg *viper.Viper, log *logrus.Logger) *sql.DB {
	dsn := url.URL{
		Scheme: "postgres",
		User: url.UserPassword(
			cfg.GetString("database.username"),
			cfg.GetString("database.password"),
		),
		Host:     fmt.Sprintf("%s:%d", cfg.GetString("database.host"), cfg.GetInt("database.port")),
		Path:     "/" + cfg.GetString("database.name"),
		RawQuery: url.Values{"sslmode": {cfg.GetString("database.sslmode")}}.Encode(),
	}

	db, err := sql.Open("pgx", dsn.String())
	if err != nil {
		log.WithError(err).Fatal("database: cannot open connection pool")
	}

	db.SetMaxIdleConns(cfg.GetInt("database.pool.idle"))
	db.SetMaxOpenConns(cfg.GetInt("database.pool.max"))
	db.SetConnMaxLifetime(time.Duration(cfg.GetInt("database.pool.lifetime_seconds")) * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.WithError(err).Fatal("database: ping failed")
	}

	log.WithField("database", cfg.GetString("database.name")).Info("database connected")

	return db
}
