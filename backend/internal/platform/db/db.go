// Package db opens PostgreSQL connections and applies goose migrations to the
// app schema. Runtime processes connect with the app_runtime role and never run
// DDL; only Migrate uses the migrator connection.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"relaylab/internal/platform/config"
	"relaylab/migrations"
)

// VersionTable is goose's bookkeeping table, kept inside the app schema.
const VersionTable = "app.goose_db_version"

// Open creates a connection pool. It does not ping; readiness reports health.
func Open(ctx context.Context, dsn config.Secret) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn.Reveal())
	if err != nil {
		return nil, errors.New("invalid database configuration") // never echo the DSN
	}
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	return pgxpool.NewWithConfig(ctx, cfg)
}

func setupGoose(logger *slog.Logger) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName(VersionTable)
	goose.SetLogger(gooseLogger{logger})
	return goose.SetDialect("postgres")
}

func openSQL(dsn config.Secret) (*sql.DB, error) {
	cfg, err := pgconnParse(dsn)
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*cfg), nil
}

// Migrate applies all pending migrations using the migrator connection.
func Migrate(ctx context.Context, dsn config.Secret, logger *slog.Logger) error {
	if err := setupGoose(logger); err != nil {
		return err
	}
	sqlDB, err := openSQL(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	return goose.UpContext(ctx, sqlDB, ".")
}

// MigrateDownAll rolls back every migration. Intended for tests and local resets.
func MigrateDownAll(ctx context.Context, dsn config.Secret, logger *slog.Logger) error {
	if err := setupGoose(logger); err != nil {
		return err
	}
	sqlDB, err := openSQL(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	return goose.DownToContext(ctx, sqlDB, ".", 0)
}

// LatestVersion returns the highest embedded migration version.
func LatestVersion() (int64, error) {
	goose.SetBaseFS(migrations.FS)
	ms, err := goose.CollectMigrations(".", 0, goose.MaxVersion)
	if err != nil {
		return 0, err
	}
	if len(ms) == 0 {
		return 0, nil
	}
	return ms[len(ms)-1].Version, nil
}

// AppliedVersion returns the highest applied migration version, or 0 if the
// version table does not exist yet. It only reads; it never creates the table.
func AppliedVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	const q = `SELECT coalesce(max(version_id), 0) FROM (
	  SELECT DISTINCT ON (version_id) version_id, is_applied
	  FROM app.goose_db_version ORDER BY version_id, id DESC
	) v WHERE is_applied`
	var v int64
	err := pool.QueryRow(ctx, q).Scan(&v)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
		return 0, nil
	}
	return v, err
}

type gooseLogger struct{ l *slog.Logger }

func (g gooseLogger) Fatalf(format string, v ...any) { g.l.Error(fmt.Sprintf(format, v...)) }
func (g gooseLogger) Printf(format string, v ...any) { g.l.Info(fmt.Sprintf(format, v...)) }
