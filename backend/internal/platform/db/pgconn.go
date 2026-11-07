package db

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"relaylab/internal/platform/config"
)

func pgconnParse(dsn config.Secret) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn.Reveal())
	if err != nil {
		return nil, errors.New("invalid database configuration") // never echo the DSN
	}
	cfg.ConnectTimeout = 5 * time.Second
	return cfg, nil
}
