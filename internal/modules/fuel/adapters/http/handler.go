// Package http translates HTTP to fuel log use cases and back.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

// Pagination and summary window.
const (
	defaultPageSize      = 30
	maxPageSize          = 100
	defaultSummaryMonths = 6
)

var errLogNotFound = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Esta carga ya no existe.")

// Handler serves the fuel routes.
type Handler struct {
	service *app.Service
}

// NewHandler creates the handler.
func NewHandler(service *app.Service) *Handler { return &Handler{service: service} }

// Routes mounts the routes; all of them demand a token.
func (h *Handler) Routes(protected gin.IRoutes) {
	protected.GET("/fuel-logs", h.list)
	protected.POST("/fuel-logs", h.add)
	protected.DELETE("/fuel-logs/:id", h.delete)
	protected.GET("/fuel/summary", h.summary)
}

type addBody struct {
	Date     string           `json:"date"`
	Odometer *decimal.Decimal `json:"odometer"`
	FuelType string           `json:"fuel_type"`
	Amount   *decimal.Decimal `json:"amount"`
	IsFull   bool             `json:"is_full"`
	// Optional: what the station charged per gallon.
	PricePerGallon *decimal.Decimal `json:"price_per_gallon"`
}

type logResponse struct {
	ID       uuid.UUID   `json:"id"`
	Date     string      `json:"date"`
	Odometer json.Number `json:"odometer"`
	FuelType string      `json:"fuel_type"`
	Amount   string      `json:"amount"`
	IsFull   bool        `json:"is_full"`
	// Null when the owner did not note it.
	PricePerGallon *string   `json:"price_per_gallon"`
	CreatedAt      time.Time `json:"created_at"`
}

type monthResponse struct {
	Month   string        `json:"month"`
	Spent   string        `json:"spent"`
	Charged string        `json:"charged"`
	Logs    []logResponse `json:"logs"`
}

type summaryResponse struct {
	Months    []monthResponse `json:"months"`
	CostPerKm *string         `json:"real_cost_per_km"`
	HasMore   bool            `json:"has_more"`
}

func (h *Handler) list(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	page, ok := intQuery(c, "page", 1, 10_000)
	if !ok {
		return
	}
	pageSize, ok := intQuery(c, "page_size", defaultPageSize, maxPageSize)
	if !ok {
		return
	}
	result, err := h.service.List(c.Request.Context(), userID, pageSize, (page-1)*pageSize)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.Page(c, toLogs(result.Logs), apierror.Meta{Page: page, PageSize: pageSize, Total: result.Total})
}

func (h *Handler) add(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body addBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	log, err := h.service.Add(c.Request.Context(), userID, app.AddInput{
		Date: body.Date, Odometer: body.Odometer, FuelType: body.FuelType, Amount: body.Amount, IsFull: body.IsFull,
		PricePerGallon: body.PricePerGallon,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.Created(c, toLog(log))
}

func (h *Handler) delete(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apierror.Respond(c, errLogNotFound)
		return
	}
	if err := h.service.Delete(c.Request.Context(), userID, id); err != nil {
		respondError(c, err)
		return
	}
	apierror.NoContent(c)
}

func (h *Handler) summary(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	months, ok := intQuery(c, "months", defaultSummaryMonths, app.MaxSummaryMonths)
	if !ok {
		return
	}
	summary, err := h.service.Summary(c.Request.Context(), userID, months)
	if err != nil {
		respondError(c, err)
		return
	}
	out := summaryResponse{Months: make([]monthResponse, len(summary.Months)), HasMore: summary.HasMore}
	for i, month := range summary.Months {
		out.Months[i] = monthResponse{
			Month: month.Month.Format("2006-01"), Spent: money.Format(month.Spent),
			Charged: money.Format(month.Charged), Logs: toLogs(month.Logs),
		}
	}
	if summary.CostPerKm != nil {
		cost := summary.CostPerKm.StringFixed(3)
		out.CostPerKm = &cost
	}
	apierror.OK(c, out)
}

func intQuery(c *gin.Context, name string, fallback, maxValue int) (int, bool) {
	text := c.Query(name)
	if text == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 || n > maxValue {
		apierror.Respond(c, apierror.Validation(apierror.FieldError{Field: name, Code: domain.CodeInvalid}))
		return 0, false
	}
	return n, true
}

func respondError(c *gin.Context, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		fields := make([]apierror.FieldError, 0, len(validation.Fields))
		for _, f := range validation.Fields {
			fields = append(fields, apierror.FieldError{Field: f.Field, Code: f.Code})
		}
		apierror.Respond(c, apierror.Validation(fields...))
	case errors.Is(err, domain.ErrNotFound):
		apierror.Respond(c, errLogNotFound)
	default:
		apierror.Respond(c, err)
	}
}

func toLogs(logs []domain.Log) []logResponse {
	out := make([]logResponse, len(logs))
	for i, log := range logs {
		out[i] = toLog(log)
	}
	return out
}

func toLog(log domain.Log) logResponse {
	return logResponse{
		ID: log.ID, Date: dates.Format(log.Date), Odometer: json.Number(log.Odometer.String()),
		FuelType: string(log.FuelType), Amount: money.Format(log.Amount), IsFull: log.IsFull,
		PricePerGallon: money.FormatOptional(log.PricePerGallon), CreatedAt: log.CreatedAt.UTC(),
	}
}
