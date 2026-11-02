// Package home is the home screen: one request with everything it shows.
package home

import (
	"time"

	"github.com/gin-gonic/gin"

	homehttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/home/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/home/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
)

// Module wires the module.
type Module struct {
	handler *homehttp.Handler
}

// New creates the module.
func New(t *trips.Module, fuel app.Fuel, c clock.Clock, loc *time.Location) *Module {
	return &Module{handler: homehttp.NewHandler(app.NewService(t, fuel, c, loc), t)}
}

// Routes mounts the module's routes.
func (m *Module) Routes(protected gin.IRoutes) { m.handler.Routes(protected) }
