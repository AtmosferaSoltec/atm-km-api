// Package health exposes the health routes.
package health

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// readyTimeout: if the database does not answer within this time, the API is not ready.
const readyTimeout = 2 * time.Second

// Pinger checks that the database answers.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Register mounts the routes on the /api/v1 group:
//
//   - GET /health: the process answers (liveness). Docker's healthcheck uses it:
//     a failing database must not restart the container in a loop.
//   - GET /health/ready: the database answers too.
func Register(routes gin.IRoutes, db Pinger) {
	routes.GET("/health", func(c *gin.Context) {
		apierror.OK(c, gin.H{"status": "ok"})
	})
	routes.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logger.From(c.Request.Context()).Warn("database not ready", "error", err)
			apierror.Respond(c, apierror.ErrDatabaseDown)
			return
		}
		apierror.OK(c, gin.H{"status": "ok", "database": "ok"})
	})
}
