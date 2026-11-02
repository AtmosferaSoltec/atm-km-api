// Package logger builds the app's slog logger and carries it in each request's
// context with its request_id, so every line of a request can be found together.
package logger

import (
	"context"
	"io"
	"log/slog"
)

type contextKey struct{}

// New creates the logger: JSON in production (read by the server's tooling) and
// text in development (read by a person).
func New(w io.Writer, production bool) *slog.Logger {
	options := &slog.HandlerOptions{Level: slog.LevelInfo}
	if production {
		return slog.New(slog.NewJSONHandler(w, options))
	}
	options.Level = slog.LevelDebug
	return slog.New(slog.NewTextHandler(w, options))
}

// WithContext stores the request's logger.
func WithContext(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, log)
}

// From returns the request's logger, or the global one if there is none.
func From(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return log
	}
	return slog.Default()
}
