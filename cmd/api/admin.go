package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/term"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/config"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/database"
	"github.com/AtmosferaSoltec/atm-km-api/internal/server"
)

// There is no sign-up endpoint: accounts are managed here, from the server.
// The password is never a flag (it would stay in the shell history): it is
// typed without echo, or piped through stdin for scripts.

const userUsage = `usage:
  api user create --username USERNAME --name "DISPLAY NAME"
  api user password --username USERNAME
  api user deactivate --username USERNAME
  api user activate --username USERNAME

The password is asked without echo, or read from stdin when it is piped:
  echo "$PASSWORD" | api user create --username joel --name "Joel"`

func userCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, userUsage)
		return 2
	}
	flags := flag.NewFlagSet("user "+args[0], flag.ContinueOnError)
	username := flags.String("username", "", "login name: 3-40 lowercase letters, digits, dot, dash or underscore (a DNI works)")
	name := flags.String("name", "", "name shown in the app and on the share page")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if *username == "" {
		fmt.Fprintln(os.Stderr, "--username is required")
		return 2
	}

	ctx := context.Background()
	app, closeApp, err := buildApp(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeApp()

	switch args[0] {
	case "create":
		password, err := readNewPassword()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		user, err := app.Identity.CreateUser(ctx, identity.CreateUserInput{Username: *username, DisplayName: *name, Password: password})
		if err != nil {
			return report(err)
		}
		fmt.Printf("user %q created (id %s)\n", user.Username, user.ID)
	case "password":
		password, err := readNewPassword()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if err := app.Identity.ChangePassword(ctx, *username, password); err != nil {
			return report(err)
		}
		fmt.Println("password changed; every session of the user was closed")
	case "deactivate", "activate":
		active := args[0] == "activate"
		if err := app.Identity.SetActive(ctx, *username, active); err != nil {
			return report(err)
		}
		fmt.Printf("user %q %sd\n", strings.ToLower(*username), args[0])
	default:
		fmt.Fprintln(os.Stderr, userUsage)
		return 2
	}
	return 0
}

// cleanup runs the housekeeping once, right now.
func cleanup() int {
	ctx := context.Background()
	app, closeApp, err := buildApp(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeApp()
	photos, err := app.Trips.CleanupPhotos(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fuelPhotos, err := app.Fuel.CleanupPhotos(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	photos += fuelPhotos
	sessions, err := app.Identity.Prune(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("deleted %d photo files/rows and %d expired session records\n", photos, sessions)
	return 0
}

func buildApp(ctx context.Context) (*server.App, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Ping(ctx, pool, 5*time.Second); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("database not reachable: %w", err)
	}
	// Commands do not serve: no need for Gin's route listing.
	gin.SetMode(gin.ReleaseMode)
	app, err := server.Build(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Options{})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return app, pool.Close, nil
}

func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd()) //nolint:gosec // a file descriptor fits an int
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat it: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the passwords do not match")
	}
	return string(first), nil
}

func report(err error) int {
	var validation *identity.ValidationError
	switch {
	case errors.As(err, &validation):
		for _, field := range validation.Fields {
			fmt.Fprintf(os.Stderr, "%s: %s\n", field.Field, explain(field.Code))
		}
	case errors.Is(err, identity.ErrUsernameTaken):
		fmt.Fprintln(os.Stderr, "that username is already taken")
	case errors.Is(err, identity.ErrNotFound):
		fmt.Fprintln(os.Stderr, "no user with that username")
	default:
		fmt.Fprintln(os.Stderr, err)
	}
	return 1
}

func explain(code string) string {
	switch code {
	case "USERNAME_INVALID":
		return "3-40 characters: lowercase letters, digits, dot, dash or underscore, starting with a letter or digit"
	case "PASSWORD_TOO_SHORT":
		return "at least 8 characters"
	case "PASSWORD_TOO_LONG":
		return "at most 128 characters"
	case "DISPLAY_NAME_REQUIRED":
		return "required (--name)"
	case "DISPLAY_NAME_TOO_LONG":
		return "at most 60 characters"
	}
	return code
}
