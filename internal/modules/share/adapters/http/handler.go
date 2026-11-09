// Package http serves the public share page.
package http

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/share/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

const codeShareNotFound = "SHARE_LINK_NOT_FOUND"

var errNotFound = apierror.New(http.StatusNotFound, codeShareNotFound, "Este enlace ya no está disponible.")

// Presenter renders trips (the trips module).
type Presenter interface {
	Present(list []trips.Trip) []trips.TripResponse
	PresentVouchers(list []trips.Voucher) []trips.VoucherResponse
}

// Handler serves GET /share/:token.
type Handler struct {
	service   *app.Service
	presenter Presenter
}

// NewHandler creates the handler.
func NewHandler(service *app.Service, presenter Presenter) *Handler {
	return &Handler{service: service, presenter: presenter}
}

// Routes mounts the route. It is public: the token is the credential.
func (h *Handler) Routes(public gin.IRoutes) {
	public.GET("/share/:token", h.get)
}

type pendingResponse struct {
	Count       int                  `json:"count"`
	TotalAmount string               `json:"total_amount"`
	Trips       []trips.TripResponse `json:"trips"`
	// Fuel vouchers already paid by the borrower and what is left to pay: trips minus vouchers.
	Vouchers      []trips.VoucherResponse `json:"vouchers"`
	VoucherAmount string                  `json:"voucher_amount"`
	Balance       string                  `json:"balance"`
}

type shareResponse struct {
	OwnerName    string               `json:"owner_name"`
	CarName      string               `json:"car_name"`
	BorrowerName string               `json:"borrower_name"`
	Pending      pendingResponse      `json:"pending"`
	History      []trips.TripResponse `json:"history"`
}

func (h *Handler) get(c *gin.Context) {
	token, err := uuid.Parse(c.Param("token"))
	if err != nil {
		apierror.Respond(c, errNotFound)
		return
	}
	page, err := h.service.Get(c.Request.Context(), token)
	if errors.Is(err, app.ErrNotFound) {
		apierror.Respond(c, errNotFound)
		return
	}
	if err != nil {
		apierror.Respond(c, err)
		return
	}
	// The borrower's page must not be cached by a shared proxy.
	c.Header("Cache-Control", "private, no-store")
	apierror.OK(c, shareResponse{
		OwnerName: page.OwnerName, CarName: page.CarName, BorrowerName: page.BorrowerName,
		Pending: pendingResponse{
			Count: len(page.Pending), TotalAmount: money.Format(page.PendingTotal), Trips: h.presenter.Present(page.Pending),
			Vouchers: h.presenter.PresentVouchers(page.Vouchers), VoucherAmount: money.Format(page.VoucherTotal),
			Balance: money.Format(page.PendingTotal.Sub(page.VoucherTotal)),
		},
		History: h.presenter.Present(page.History),
	})
}
