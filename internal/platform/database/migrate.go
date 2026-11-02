package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	// pgx's database/sql driver, for goose.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Commands accepted by the migrate subcommand.
const (
	MigrateUp     = "up"
	MigrateDown   = "down"
	MigrateStatus = "status"
)

// Migrate applies (up), reverts the last (down) or lists (status) the
// migrations. The goose_db_version control table lives in the km schema,
// because it is the role's search_path.
func Migrate(ctx context.Context, databaseURL string, migrations fs.FS, command string, log *slog.Logger) error {
	conn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: invalid url")
	}
	defer func() { _ = conn.Close() }()

	goose.SetBaseFS(migrations)
	goose.SetLogger(gooseLogger{log: log})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if _, err := goose.EnsureDBVersionContext(ctx, conn); err != nil {
		return fmt.Errorf("ensure goose version table: %w", err)
	}

	switch command {
	case MigrateUp:
		err = goose.UpContext(ctx, conn, "migrations")
	case MigrateDown:
		err = goose.DownContext(ctx, conn, "migrations")
	case MigrateStatus:
		err = goose.StatusContext(ctx, conn, "migrations")
	default:
		return fmt.Errorf("unknown migrate command %q (use up, down or status)", command)
	}
	if errors.Is(err, goose.ErrNoMigrationFiles) {
		log.Info("no migrations to apply")
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", command, err)
	}
	return nil
}

// gooseLogger sends goose's messages to slog.
type gooseLogger struct {
	log *slog.Logger
}

func (l gooseLogger) Printf(format string, v ...any) {
	l.log.Info(fmt.Sprintf(format, v...))
}

func (l gooseLogger) Fatalf(format string, v ...any) {
	l.log.Error(fmt.Sprintf(format, v...))
}
