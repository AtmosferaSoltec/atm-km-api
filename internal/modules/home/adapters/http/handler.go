// Package http serves the home screen.
package http

import (
	"encoding/json"
	"strconv"

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
	PresentVouchers(list []trips.Voucher) []trips.VoucherResponse
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
	protected.GET("/usage", h.usage)
}

type weekUsageResponse struct {
	Start      string      `json:"start"`
	OwnerKm    json.Number `json:"owner_km"`
	BorrowerKm json.Number `json:"borrower_km"`
}

type usageResponse struct {
	Weeks         []weekUsageResponse `json:"weeks"`
	OwnerKm       json.Number         `json:"owner_km"`
	BorrowerKm    json.Number         `json:"borrower_km"`
	OwnerSharePct int                 `json:"owner_share_pct"`
}

func (h *Handler) usage(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	weeks := app.DefaultUsageWeeks
	if text := c.Query("weeks"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > app.MaxUsageWeeks {
			apierror.Respond(c, apierror.Validation(apierror.FieldError{Field: "weeks", Code: "INVALID"}))
			return
		}
		weeks = n
	}
	usage, err := h.service.Usage(c.Request.Context(), userID, weeks)
	if err != nil {
		apierror.Respond(c, err)
		return
	}
	out := usageResponse{
		Weeks:   make([]weekUsageResponse, len(usage.Weeks)),
		OwnerKm: json.Number(usage.OwnerKm.String()), BorrowerKm: json.Number(usage.BorrowerKm.String()),
		OwnerSharePct: usage.OwnerSharePct,
	}
	for i, week := range usage.Weeks {
		out.Weeks[i] = weekUsageResponse{
			Start: dates.Format(week.Start), OwnerKm: json.Number(week.OwnerKm.String()), BorrowerKm: json.Number(week.BorrowerKm.String()),
		}
	}
	apierror.OK(c, out)
}

type pendingResponse struct {
	Count       int                  `json:"count"`
	TotalAmount string               `json:"total_amount"`
	Trips       []trips.TripResponse `json:"trips"`
	// Vouchers the borrower paid and that are not yet discounted, and what is
	// left to pay once they are: trips minus vouchers (negative if they exceed it).
	Vouchers      []trips.VoucherResponse `json:"vouchers"`
	VoucherAmount string                  `json:"voucher_amount"`
	Balance       string                  `json:"balance"`
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
			Vouchers: h.presenter.PresentVouchers(home.Vouchers), VoucherAmount: money.Format(home.VoucherTotal),
			Balance: money.Format(home.PendingTotal.Sub(home.VoucherTotal)),
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
