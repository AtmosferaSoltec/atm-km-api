// Package httpserver builds Gin with the shared middlewares and runs the HTTP
// server with time limits and an orderly shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/middleware"
)

// APIPrefix is the prefix of every route.
const APIPrefix = "/api/v1"

// maxBodyBytes: no JSON body of this API comes close; more is abuse.
const maxBodyBytes = 1 << 20

// X-Forwarded-For is only trusted when the request comes from here: the host's
// nginx and the Docker network. Nobody on the internet can fake their IP.
var trustedProxies = []string{"127.0.0.1", "::1", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

// Options is what the router needs.
type Options struct {
	Production         bool
	Logger             *slog.Logger
	CORSOrigins        []string
	RateLimitPerMinute int
	// LargeBodyRoutes are routes (relative to /api/v1, as modules mount them)
	// that accept a larger body than the general limit, with their own limit.
	LargeBodyRoutes map[string]int64
}

// NewRouter returns Gin with the shared middlewares and the /api/v1 group,
// where each module mounts its routes.
func NewRouter(opts Options) (*gin.Engine, *gin.RouterGroup, error) {
	if opts.Production {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RemoteIPHeaders = []string{"X-Forwarded-For"}
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		return nil, nil, fmt.Errorf("set trusted proxies: %w", err)
	}

	router.Use(
		middleware.RequestID(opts.Logger),
		middleware.Recover(),
		middleware.AccessLog(),
		middleware.CORS(opts.CORSOrigins),
		middleware.BodyLimit(maxBodyBytes, prefixed(opts.LargeBodyRoutes)),
		middleware.NewRateLimiter(opts.RateLimitPerMinute).Middleware(),
	)
	router.NoRoute(func(c *gin.Context) { apierror.Respond(c, apierror.ErrNotFound) })
	router.NoMethod(func(c *gin.Context) { apierror.Respond(c, apierror.ErrMethodNotAllowed) })

	return router, router.Group(APIPrefix), nil
}

// Serve listens on port until ctx is cancelled, then shuts down in order: it
// stops accepting connections and waits up to 10 s for ongoing requests.
func Serve(ctx context.Context, handler http.Handler, port string, log *slog.Logger) error {
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// Photo uploads from a phone on mobile data need more than a JSON body.
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	failed := make(chan error, 1)
	go func() {
		log.Info("server started", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
		close(failed)
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("server stopped: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func prefixed(routes map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(routes))
	for route, limit := range routes {
		out[APIPrefix+route] = limit
	}
	return out
}
