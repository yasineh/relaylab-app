package health

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"relaylab/internal/platform/db"
	"relaylab/internal/platform/db/store"
)

// ErrPendingMigrations means the database is behind the embedded migrations.
var ErrPendingMigrations = errors.New("pending migrations")

// PostgresChecks returns the database and migration checks for pool.
func PostgresChecks(pool *pgxpool.Pool) (database, migrations Check, err error) {
	latest, err := db.LatestVersion()
	if err != nil {
		return nil, nil, err
	}
	q := store.New(pool)
	database = func(ctx context.Context) error {
		_, err := q.Ping(ctx)
		return err
	}
	migrations = func(ctx context.Context) error {
		applied, err := db.AppliedVersion(ctx, pool)
		if err != nil {
			return err
		}
		if applied < latest {
			return ErrPendingMigrations
		}
		return nil
	}
	return database, migrations, nil
}
