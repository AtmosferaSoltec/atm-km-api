// Package testsupport has helpers for the integration tests.
package testsupport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/AtmosferaSoltec/atm-km-api/db"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/database"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
)

// DatabaseURLVar points to the local test database. The deploy's check has no
// database: without this variable the integration tests are skipped.
const DatabaseURLVar = "TEST_DATABASE_URL"

// DatabaseURL returns the test database URL, or skips the test.
func DatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv(DatabaseURLVar)
	if url == "" {
		t.Skipf("%s is not set: integration test skipped", DatabaseURLVar)
	}
	return url
}

// Pool opens a pool against the test database and closes it at the end.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Open(context.Background(), DatabaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

var migrateOnce sync.Once
var migrateErr error

// MigratedPool applies pending migrations (once per test package) and opens a
// pool. Module tests start from here.
func MigratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := DatabaseURL(t)
	migrateOnce.Do(func() {
		quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
		migrateErr = database.Migrate(context.Background(), url, db.Migrations, database.MigrateUp, quiet)
	})
	require.NoError(t, migrateErr)
	return Pool(t)
}

// UniqueUsername gives a username that does not clash with other local data.
func UniqueUsername(label string) string {
	return label + "-" + ids.New().String()[24:]
}

// DeleteUsers removes the test accounts at the end; the cascade takes the rest.
func DeleteUsers(t *testing.T, pool *pgxpool.Pool, usernames ...string) {
	t.Helper()
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `DELETE FROM users WHERE username = ANY($1)`, usernames)
		if err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})
}
