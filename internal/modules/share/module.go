// Package share is the page the borrower opens from the share link, with no
// login. It shows what is owed and the trips' history, never the fuel log.
package share

import (
	"github.com/gin-gonic/gin"

	sharehttp "github.com/AtmosferaSoltec/atm-km-api/internal/modules/share/adapters/http"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/share/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
)

// Module wires the module.
type Module struct {
	handler *sharehttp.Handler
}

// New creates the module.
func New(settings app.Settings, owners app.Owners, t *trips.Module) *Module {
	return &Module{handler: sharehttp.NewHandler(app.NewService(settings, owners, t), t)}
}

// Routes mounts the module's routes.
func (m *Module) Routes(public gin.IRoutes) { m.handler.Routes(public) }
