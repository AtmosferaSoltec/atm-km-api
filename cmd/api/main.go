// The API's single entry point: it loads the configuration, wires the modules
// and starts Gin. The same binary carries the subcommands Docker, the
// production compose and the owner use:
//
//	api                                   start the server
//	api migrate [up|down|status]
//	api healthcheck                       used by the image's HEALTHCHECK (no curl in it)
//	api user create --username U --name N create an account (asks for the password)
//	api user password --username U        set a new password (closes every session)
//	api user deactivate|activate --username U
//	api cleanup                           delete expired photos and sessions now
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// The image ships tzdata, but this way "today" never depends on the system.
	_ "time/tzdata"

	"github.com/AtmosferaSoltec/atm-km-api/db"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/config"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/database"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/httpserver"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
	"github.com/AtmosferaSoltec/atm-km-api/internal/server"
)

// housekeepingInterval: photos expire by the day, so a few runs a day is plenty.
const housekeepingInterval = 6 * time.Hour

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "migrate":
			os.Exit(migrate(os.Args[2:]))
		case "user":
			os.Exit(userCommand(os.Args[2:]))
		case "cleanup":
			os.Exit(cleanup())
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	log := logger.New(os.Stdout, cfg.IsProduction())
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database", "error", err)
		return 1
	}
	defer pool.Close()
	// The database is not required to start: /health/ready says whether it
	// answers, and the pool reconnects on its own when it comes back.
	if err := database.Ping(ctx, pool, 5*time.Second); err != nil {
		log.Warn("database not reachable at startup", "error", err)
	}

	app, err := server.Build(cfg, pool, log, server.Options{})
	if err != nil {
		log.Error("modules", "error", err)
		return 1
	}
	go app.Housekeeping(ctx, log, housekeepingInterval)

	if err := httpserver.Serve(ctx, app.Router, cfg.Port, log); err != nil {
		log.Error("server", "error", err)
		return 1
	}
	return 0
}

// migrate is run by the compose's migrate service before the app starts. It
// only needs DATABASE_URL.
func migrate(args []string) int {
	log := logger.New(os.Stdout, os.Getenv("APP_ENV") == string(config.Production))
	command := database.MigrateUp
	if len(args) > 0 {
		command = args[0]
	}
	databaseURL, err := config.DatabaseURL()
	if err != nil {
		log.Error("migrate", "error", err)
		return 1
	}
	if err := database.Migrate(context.Background(), databaseURL, db.Migrations, command, log); err != nil {
		log.Error("migrate", "error", err)
		return 1
	}
	return 0
}

func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port() + httpserver.APIPrefix + "/health")
	if err != nil {
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "3001"
}
