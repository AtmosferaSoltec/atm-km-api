// Package http translates HTTP to settings use cases and back.
package http

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
)

// Handler serves the settings routes.
type Handler struct {
	service *app.Service
}

// NewHandler creates the handler.
func NewHandler(service *app.Service) *Handler { return &Handler{service: service} }

// Routes mounts the routes; all of them demand a token.
func (h *Handler) Routes(protected gin.IRoutes) {
	protected.GET("/settings", h.get)
	protected.PUT("/settings/rates", h.updateRates)
	protected.PUT("/settings/profile", h.updateProfile)
	protected.POST("/settings/share-token", h.rotateShareToken)
}

type ratesBody struct {
	GasolineRate string `json:"gasoline_rate"`
	LPGRate      string `json:"lpg_rate"`
	WearRate     string `json:"wear_rate"`
}

type profileBody struct {
	CarName      string `json:"car_name"`
	BorrowerName string `json:"borrower_name"`
}

type settingsResponse struct {
	CarName      string    `json:"car_name"`
	BorrowerName string    `json:"borrower_name"`
	Rates        ratesBody `json:"rates"`
	ShareToken   uuid.UUID `json:"share_token"`
}

func (h *Handler) get(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	settings, err := h.service.Get(c.Request.Context(), userID)
	respond(c, settings, err)
}

func (h *Handler) updateRates(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body ratesBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	settings, err := h.service.UpdateRates(c.Request.Context(), userID, app.RatesInput{
		Gasoline: body.GasolineRate, LPG: body.LPGRate, Wear: body.WearRate,
	})
	respond(c, settings, err)
}

func (h *Handler) updateProfile(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body profileBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	settings, err := h.service.UpdateProfile(c.Request.Context(), userID, body.CarName, body.BorrowerName)
	respond(c, settings, err)
}

func (h *Handler) rotateShareToken(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	settings, err := h.service.RotateShareToken(c.Request.Context(), userID)
	respond(c, settings, err)
}

func respond(c *gin.Context, settings domain.Settings, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		fields := make([]apierror.FieldError, 0, len(validation.Fields))
		for _, f := range validation.Fields {
			fields = append(fields, apierror.FieldError{Field: f.Field, Code: f.Code})
		}
		apierror.Respond(c, apierror.Validation(fields...))
	case err != nil:
		apierror.Respond(c, err)
	default:
		apierror.OK(c, toResponse(settings))
	}
}

// toResponse renders settings for the API. Rates keep their three decimals.
func toResponse(s domain.Settings) settingsResponse {
	return settingsResponse{
		CarName:      s.CarName,
		BorrowerName: s.BorrowerName,
		Rates: ratesBody{
			GasolineRate: s.Rates.Gasoline.StringFixed(3),
			LPGRate:      s.Rates.LPG.StringFixed(3),
			WearRate:     s.Rates.Wear.StringFixed(3),
		},
		ShareToken: s.ShareToken,
	}
}
