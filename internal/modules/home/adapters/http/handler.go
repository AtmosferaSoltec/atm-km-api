// Package http serves the home screen.
package http

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/home/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

// Presenter renders trips (the trips module).
type Presenter interface {
	Present(list []trips.Trip) []trips.TripResponse
	PresentOne(trip trips.Trip) trips.TripResponse
}

// Handler serves GET /home.
type Handler struct {
	service   *app.Service
	presenter Presenter
}

// NewHandler creates the handler.
func NewHandler(service *app.Service, presenter Presenter) *Handler {
	return &Handler{service: service, presenter: presenter}
}

// Routes mounts the route.
func (h *Handler) Routes(protected gin.IRoutes) {
	protected.GET("/home", h.get)
}

type pendingResponse struct {
	Count       int                  `json:"count"`
	TotalAmount string               `json:"total_amount"`
	Trips       []trips.TripResponse `json:"trips"`
}

type recentResponse struct {
	Since      string      `json:"since"`
	DistanceKm json.Number `json:"distance_km"`
	Charged    string      `json:"charged"`
	FuelSpent  string      `json:"fuel_spent"`
	Trips      int64       `json:"trips"`
}

type homeResponse struct {
	OpenTrip     *trips.TripResponse  `json:"open_trip"`
	Pending      pendingResponse      `json:"pending"`
	Last30Days   recentResponse       `json:"last_30_days"`
	RecentTrips  []trips.TripResponse `json:"recent_trips"`
	LastOdometer json.Number          `json:"last_odometer"`
	LastFuelType *trips.FuelType      `json:"last_fuel_type"`
}

func (h *Handler) get(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	home, err := h.service.Get(c.Request.Context(), userID)
	if err != nil {
		apierror.Respond(c, err)
		return
	}
	out := homeResponse{
		Pending: pendingResponse{
			Count: len(home.Pending), TotalAmount: money.Format(home.PendingTotal), Trips: h.presenter.Present(home.Pending),
		},
		Last30Days: recentResponse{
			Since: dates.Format(home.Since), DistanceKm: json.Number(home.Recent.DistanceKm.String()),
			Charged: money.Format(home.Recent.Charged), FuelSpent: money.Format(home.FuelSpent), Trips: home.Recent.Trips,
		},
		RecentTrips:  h.presenter.Present(home.RecentTrips),
		LastOdometer: json.Number(home.LastOdometer.String()),
		LastFuelType: home.LastFuelType,
	}
	if home.OpenTrip != nil {
		open := h.presenter.PresentOne(*home.OpenTrip)
		out.OpenTrip = &open
	}
	apierror.OK(c, out)
}
