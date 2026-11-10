// Package http translates HTTP to fuel log use cases and back.
package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

// Pagination and summary window.
const (
	defaultPageSize      = 30
	maxPageSize          = 100
	defaultSummaryMonths = 6
)

// photoURLLifetime: long enough for a page left open, short enough that a
// leaked URL stops working the same day. Expiry is rounded to the hour so the
// URL (and the browser cache) stays the same for an hour.
const photoURLLifetime = 12 * time.Hour

// PhotoRoute is the receipt photo's route, relative to /api/v1: PUT takes raw
// bytes (main gives it a larger body limit), DELETE removes it and a signed GET serves it.
const PhotoRoute = "/fuel-logs/:id/photo"

var (
	errLogNotFound   = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Este registro ya no existe.")
	errPhotoNotFound = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Esta foto ya no existe.")
)

// Handler serves the fuel routes.
type Handler struct {
	service   *app.Service
	signer    photostore.Signer
	clock     clock.Clock
	retention time.Duration
}

// NewHandler creates the handler. The signer, clock and retention are for the receipt photos' URLs.
func NewHandler(service *app.Service, signer photostore.Signer, clock clock.Clock, retention time.Duration) *Handler {
	return &Handler{service: service, signer: signer, clock: clock, retention: retention}
}

// Routes mounts the routes. All demand a token but the photo itself, whose
// signed URL is the credential (an <img> cannot send a header).
func (h *Handler) Routes(public, protected gin.IRoutes) {
	protected.GET("/fuel-logs", h.list)
	protected.POST("/fuel-logs", h.add)
	protected.PUT("/fuel-logs/:id", h.update)
	protected.DELETE("/fuel-logs/:id", h.delete)
	protected.PUT(PhotoRoute, h.setPhoto)
	protected.DELETE(PhotoRoute, h.deletePhoto)
	public.GET(PhotoRoute, h.servePhoto)
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
	PricePerGallon *string `json:"price_per_gallon"`
	// Null without a receipt photo (or once the cleanup deleted it).
	Photo     *photoResponse `json:"photo"`
	CreatedAt time.Time      `json:"created_at"`
}

// photoResponse is a photo: a signed URL, when it was taken and when the cleanup deletes it.
type photoResponse struct {
	URL       string    `json:"url"`
	TakenAt   time.Time `json:"taken_at"`
	ExpiresAt time.Time `json:"expires_at"`
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
	apierror.Page(c, h.toLogs(result.Logs), apierror.Meta{Page: page, PageSize: pageSize, Total: result.Total})
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
	apierror.Created(c, h.toLog(log))
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
			Charged: money.Format(month.Charged), Logs: h.toLogs(month.Logs),
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
	case errors.Is(err, domain.ErrNoPhoto):
		apierror.Respond(c, errPhotoNotFound)
	default:
		apierror.Respond(c, err)
	}
}

func (h *Handler) toLogs(logs []domain.Log) []logResponse {
	out := make([]logResponse, len(logs))
	for i, log := range logs {
		out[i] = h.toLog(log)
	}
	return out
}

func (h *Handler) toLog(log domain.Log) logResponse {
	out := logResponse{
		ID: log.ID, Date: dates.Format(log.Date), Odometer: json.Number(log.Odometer.String()),
		FuelType: string(log.FuelType), Amount: money.Format(log.Amount), IsFull: log.IsFull,
		PricePerGallon: money.FormatOptional(log.PricePerGallon), CreatedAt: log.CreatedAt.UTC(),
	}
	if log.Photo != nil {
		expires := h.clock.Now().Truncate(time.Hour).Add(photoURLLifetime + time.Hour)
		signed := h.signer.Sign(log.ID.String(), expires)
		query := url.Values{"exp": {fmt.Sprint(signed.Expires)}, "sig": {signed.Signature}}
		out.Photo = &photoResponse{
			URL:       "/api/v1/fuel-logs/" + log.ID.String() + "/photo?" + query.Encode(),
			TakenAt:   log.Photo.CreatedAt.UTC(),
			ExpiresAt: log.Photo.CreatedAt.Add(h.retention).UTC(),
		}
	}
	return out
}

func (h *Handler) update(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	id, ok := logID(c)
	if !ok {
		return
	}
	var body addBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	log, err := h.service.Update(c.Request.Context(), userID, id, app.AddInput{
		Date: body.Date, Odometer: body.Odometer, FuelType: body.FuelType, Amount: body.Amount, IsFull: body.IsFull,
		PricePerGallon: body.PricePerGallon,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, h.toLog(log))
}

func (h *Handler) setPhoto(c *gin.Context) {
	h.withLog(c, func(userID, id uuid.UUID) (domain.Log, error) {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return domain.Log{}, err
		}
		return h.service.SetPhoto(c.Request.Context(), userID, id, data)
	})
}

func (h *Handler) deletePhoto(c *gin.Context) {
	h.withLog(c, func(userID, id uuid.UUID) (domain.Log, error) {
		return h.service.DeletePhoto(c.Request.Context(), userID, id)
	})
}

func (h *Handler) withLog(c *gin.Context, action func(userID, id uuid.UUID) (domain.Log, error)) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	id, ok := logID(c)
	if !ok {
		return
	}
	log, err := action(userID, id)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, h.toLog(log))
}

// servePhoto streams a photo whose URL signature is valid. A bad signature and
// a missing photo answer the same: nothing to probe.
func (h *Handler) servePhoto(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || !h.signer.Valid(id.String(), c.Query("exp"), c.Query("sig"), h.clock.Now()) {
		apierror.Respond(c, errPhotoNotFound)
		return
	}
	photo, file, err := h.service.OpenPhoto(c.Request.Context(), id)
	if err != nil {
		respondError(c, err)
		return
	}
	defer file.Close() //nolint:errcheck // read-only
	expires, _ := strconv.ParseInt(c.Query("exp"), 10, 64)
	maxAge := max(time.Until(time.Unix(expires, 0)), 0)
	c.Header("Content-Type", photo.ContentType)
	c.Header("Cache-Control", "private, max-age="+strconv.Itoa(int(maxAge.Seconds())))
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, "", photo.CreatedAt, file)
}

func logID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apierror.Respond(c, errLogNotFound)
		return uuid.Nil, false
	}
	return id, true
}
