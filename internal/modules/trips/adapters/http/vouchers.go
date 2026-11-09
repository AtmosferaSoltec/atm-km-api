package http

import (
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
)

type voucherBody struct {
	Date   string           `json:"date"`
	Amount *decimal.Decimal `json:"amount"`
}

// listVouchers returns the credit (unsettled) by default, or the settled ones
// with ?settled=true.
func (h *Handler) listVouchers(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	settled := false
	if text := c.Query("settled"); text != "" {
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			apierror.Respond(c, apierror.Validation(apierror.FieldError{Field: "settled", Code: domain.CodeInvalid}))
			return
		}
		settled = parsed
	}
	vouchers, err := h.service.Vouchers(c.Request.Context(), userID, settled)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, h.presenter.Vouchers(vouchers))
}

func (h *Handler) addVoucher(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	var body voucherBody
	if !apierror.BindJSON(c, &body) {
		return
	}
	voucher, err := h.service.AddVoucher(c.Request.Context(), userID, app.VoucherInput{Date: body.Date, Amount: body.Amount})
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.Created(c, h.presenter.Voucher(voucher))
}

func (h *Handler) deleteVoucher(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id", errVoucherNotFound)
	if !ok {
		return
	}
	if err := h.service.DeleteVoucher(c.Request.Context(), userID, id); err != nil {
		respondError(c, err)
		return
	}
	apierror.NoContent(c)
}

func (h *Handler) setVoucherPhoto(c *gin.Context) {
	h.withVoucher(c, func(userID, id uuid.UUID) (domain.Voucher, error) {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return domain.Voucher{}, err
		}
		return h.service.SetVoucherPhoto(c.Request.Context(), userID, id, data)
	})
}

func (h *Handler) deleteVoucherPhoto(c *gin.Context) {
	h.withVoucher(c, func(userID, id uuid.UUID) (domain.Voucher, error) {
		return h.service.DeleteVoucherPhoto(c.Request.Context(), userID, id)
	})
}

func (h *Handler) withVoucher(c *gin.Context, action func(userID, id uuid.UUID) (domain.Voucher, error)) {
	userID, ok := auth.UserID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id", errVoucherNotFound)
	if !ok {
		return
	}
	voucher, err := action(userID, id)
	if err != nil {
		respondError(c, err)
		return
	}
	apierror.OK(c, h.presenter.Voucher(voucher))
}

// serveVoucherPhoto streams a voucher photo whose URL signature is valid. A bad
// signature and a missing photo answer the same: nothing to probe.
func (h *Handler) serveVoucherPhoto(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || !h.presenter.Signer.Valid(id.String(), c.Query("exp"), c.Query("sig"), h.presenter.Clock.Now()) {
		apierror.Respond(c, errVoucherNotFound)
		return
	}
	photo, file, err := h.service.OpenVoucherPhoto(c.Request.Context(), id)
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
