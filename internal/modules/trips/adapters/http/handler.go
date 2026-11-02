// Package http translates HTTP to trips use cases and back.
package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/app"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/apierror"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/auth"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

// Pagination.
const (
	defaultPageSize = 30
	maxPageSize     = 100
)

// PhotoUploadRoute is the route that takes a photo's raw bytes, relative to
// /api/v1. It gets its own body limit.
const PhotoUploadRoute = "/trips/:id/photos/:leg"

// The module's own codes.
const (
	codeAlreadyLent  = "CAR_ALREADY_LENT"
	codeWrongStatus  = "TRIP_STATUS_CONFLICT"
	codePhotoNotOpen = "TRIP_NOT_RETURNED"
)

var (
	errTripNotFound  = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Este viaje ya no existe.")
	errPhotoNotFound = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Esta foto ya no existe.")
	errAlreadyLent   = apierror.New(http.StatusConflict, codeAlreadyLent, "El auto ya está prestado. Recíbelo antes de prestarlo otra vez.")
	errWrongStatus   = apierror.New(http.StatusConflict, codeWrongStatus, "El viaje cambió de estado. Actualiza la pantalla.")
	errPhotoNotOpen  = apierror.New(http.StatusConflict, codePhotoNotOpen, "La foto de devolución va después de recibir el auto.")
)

// Handler serves the trips routes.
type Handler struct {
	service   *app.Service
	presenter Presenter
}

// NewHandler creates the handler.
func NewHandler(service *app.Service, presenter Presenter) *Handler {
	return &Handler{service: service, presenter: presenter}
}

// Routes mounts the routes. Photos are served from public routes: their
// signed URL is what authorizes them.
func (h *Handler) Routes(public, protected gin.IRoutes) {
	protected.GET("/trips", h.list)
	protected.GET("/trips/open", h.open)
	protected.POST("/trips", h.lend)
	protected.POST("/trips/pay", h.payMany)
	protected.GET("/trips/:id", h.get)
	protected.DELETE("/trips/:id", h.delete)
	protected.POST("/trips/:id/return", h.receive)
	protected.POST("/trips/:id/cancel", h.cancel)
	protected.POST("/trips/:id/pay", h.pay)
	protected.POST("/trips/:id/unpay", h.unpay)
	protected.PUT(PhotoUploadRoute, h.setPhoto)
	protected.DELETE(PhotoUploadRoute, h.deletePhoto)
	public.GET(PhotosPath+"/:photoID", h.servePhoto)
}

type returnBody struct {
	ReturnDate string           `json:"return_date"`
	EndKm      *decimal.Decimal `json:"end_km"`
	FuelType   string           `json:"fuel_type"`
}

type lendBody struct {
	StartDate string           `json:"start_date"`
	StartKm   *decimal.Decimal `json:"start_km"`
	// Return registers a trip that is already over.
	Return *returnBody `json:"return"`
}

type payBody struct {
	TripIDs []uuid.UUID `json:"trip_ids"`
}

type summary struct {
	Count       int64  `json:"count"`
	TotalAmount string `json:"total_amount"`
}

func (h *Handler) list(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var status *domain.Status
	if text := c.Query("status"); text != "" {
		parsed, ok := domain.ParseStatus(text)
		if !ok {
			apierror.Respond(c, apierror.Validation(apierror.FieldError{Field: "status", Code: domain.CodeInvalid}))
			return
		}
		status = &parsed
	}
	page, pageSize, ok := pagination(c)
	if !ok {
		return
	}
	result, err := h.service.List(c.Request.Context(), userID, status, pageSize, (page-1)*pageSize)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.Page(c, h.presenter.Trips(result.Trips), apierror.Meta{Page: page, PageSize: pageSize, Total: result.Total})
}

func (h *Handler) open(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	trip, err := h.service.Open(c.Request.Context(), userID)
	if err != nil {
		respondError(c, err)
		return
	}
	if trip == nil {
		apierror.OK(c, nil)
		return
	}
	apierror.OK(c, h.presenter.Trip(*trip))
}

func (h *Handler) get(c *gin.Context) {
	h.withTrip(c, func(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
		return h.service.Get(ctx, userID, tripID)
	})
}

func (h *Handler) lend(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body lendBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	in := app.LendInput{StartDate: body.StartDate, StartKm: body.StartKm}
	if body.Return != nil {
		in.Return = &app.ReturnInput{Date: body.Return.ReturnDate, EndKm: body.Return.EndKm, FuelType: body.Return.FuelType}
	}
	trip, err := h.service.Lend(c.Request.Context(), userID, in)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.Created(c, h.presenter.Trip(trip))
}

func (h *Handler) receive(c *gin.Context) {
	var body returnBody
	h.withTrip(c, func(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
		if !apierror.BindJSON(c, &body) {
			return domain.Trip{}, errResponded
		}
		return h.service.Receive(ctx, userID, tripID, app.ReturnInput{Date: body.ReturnDate, EndKm: body.EndKm, FuelType: body.FuelType})
	})
}

func (h *Handler) cancel(c *gin.Context) { h.withTrip(c, h.service.Cancel) }
func (h *Handler) pay(c *gin.Context)    { h.withTrip(c, h.service.MarkPaid) }
func (h *Handler) unpay(c *gin.Context)  { h.withTrip(c, h.service.MarkPending) }

func (h *Handler) payMany(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body payBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	result, err := h.service.PayMany(c.Request.Context(), userID, body.TripIDs)
	if err != nil {
		respondError(c, err)
		return
	}
	ids := result.TripIDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	apierror.OK(c, gin.H{"trip_ids": ids, "summary": summary{Count: int64(len(ids)), TotalAmount: money.Format(result.Total)}})
}

func (h *Handler) delete(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	tripID, ok := uuidParam(c, "id", errTripNotFound)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), userID, tripID); err != nil {
		respondError(c, err)
		return
	}
	apierror.NoContent(c)
}

func (h *Handler) setPhoto(c *gin.Context) {
	leg, ok := legParam(c)
	if !ok {
		return
	}
	h.withTrip(c, func(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return domain.Trip{}, err
		}
		return h.service.SetPhoto(ctx, userID, tripID, leg, data)
	})
}

func (h *Handler) deletePhoto(c *gin.Context) {
	leg, ok := legParam(c)
	if !ok {
		return
	}
	h.withTrip(c, func(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
		return h.service.DeletePhoto(ctx, userID, tripID, leg)
	})
}

// servePhoto streams a photo whose URL signature is valid. Answers are the
// same for a bad signature and a missing photo: nothing to probe.
func (h *Handler) servePhoto(c *gin.Context) {
	photoID, err := uuid.Parse(c.Param("photoID"))
	if err != nil || !h.presenter.Signer.Valid(photoID.String(), c.Query("exp"), c.Query("sig"), h.presenter.Clock.Now()) {
		apierror.Respond(c, errPhotoNotFound)
		return
	}
	photo, file, err := h.service.OpenPhoto(c.Request.Context(), photoID)
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

// errResponded: the handler already answered (a malformed body).
var errResponded = errors.New("already responded")

func (h *Handler) withTrip(c *gin.Context, action func(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error)) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	tripID, ok := uuidParam(c, "id", errTripNotFound)
	if !ok {
		return
	}
	trip, err := action(c.Request.Context(), userID, tripID)
	if errors.Is(err, errResponded) {
		return
	}
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, h.presenter.Trip(trip))
}

func uuidParam(c *gin.Context, name string, notFound *apierror.Error) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		apierror.Respond(c, notFound)
		return uuid.Nil, false
	}
	return id, true
}

func legParam(c *gin.Context) (domain.Leg, bool) {
	leg, ok := domain.ParseLeg(c.Param("leg"))
	if !ok {
		apierror.Respond(c, apierror.ErrNotFound)
	}
	return leg, ok
}

func pagination(c *gin.Context) (page, pageSize int, ok bool) {
	page, pageSize = 1, defaultPageSize
	var fields []apierror.FieldError
	if text := c.Query("page"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 10_000 {
			fields = append(fields, apierror.FieldError{Field: "page", Code: domain.CodeInvalid})
		}
		page = n
	}
	if text := c.Query("page_size"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > maxPageSize {
			fields = append(fields, apierror.FieldError{Field: "page_size", Code: domain.CodeInvalid})
		}
		pageSize = n
	}
	if len(fields) > 0 {
		apierror.Respond(c, apierror.Validation(fields...))
		return 0, 0, false
	}
	return page, pageSize, true
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
		apierror.Respond(c, errTripNotFound)
	case errors.Is(err, domain.ErrNoPhoto):
		apierror.Respond(c, errPhotoNotFound)
	case errors.Is(err, domain.ErrAlreadyLent):
		apierror.Respond(c, errAlreadyLent)
	case errors.Is(err, domain.ErrWrongStatus):
		apierror.Respond(c, errWrongStatus)
	case errors.Is(err, domain.ErrPhotoLegClosed):
		apierror.Respond(c, errPhotoNotOpen)
	default:
		apierror.Respond(c, err)
	}
}
