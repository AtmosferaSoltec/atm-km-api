package http

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/integrations/photostore"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/money"
)

// photoURLLifetime: long enough for a page left open, short enough that a
// leaked URL stops working the same day. Expiry is rounded to the hour so the
// URL (and the browser cache) stays the same for an hour.
const photoURLLifetime = 12 * time.Hour

// PhotosPath is where photos are served, relative to /api/v1.
const PhotosPath = "/photos"

// TripResponse is a trip as the API sends it. Km travel as JSON numbers, money
// and rates as strings.
type TripResponse struct {
	ID          uuid.UUID     `json:"id"`
	Status      domain.Status `json:"status"`
	StartDate   string        `json:"start_date"`
	StartKm     json.Number   `json:"start_km"`
	ReturnDate  *string       `json:"return_date"`
	EndKm       *json.Number  `json:"end_km"`
	DistanceKm  *json.Number  `json:"distance_km"`
	FuelType    *string       `json:"fuel_type"`
	FuelRate    *string       `json:"fuel_rate"`
	WearRate    *string       `json:"wear_rate"`
	FuelAmount  *string       `json:"fuel_amount"`
	WearAmount  *string       `json:"wear_amount"`
	TotalAmount *string       `json:"total_amount"`
	PaidDate    *string       `json:"paid_date"`
	Photos      PhotosBody    `json:"photos"`
	CreatedAt   time.Time     `json:"created_at"`
}

// PhotosBody holds each leg's photo, or null.
type PhotosBody struct {
	Start *PhotoBody `json:"start"`
	End   *PhotoBody `json:"end"`
}

// PhotoBody is a photo: a signed URL, when it was taken and when the cleanup
// deletes it.
type PhotoBody struct {
	URL       string    `json:"url"`
	TakenAt   time.Time `json:"taken_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Presenter renders trips for the API, signing their photo URLs.
type Presenter struct {
	Signer    photostore.Signer
	Clock     clock.Clock
	Retention time.Duration
}

// Trip renders one trip.
func (p Presenter) Trip(trip domain.Trip) TripResponse {
	out := TripResponse{
		ID: trip.ID, Status: trip.Status,
		StartDate: dates.Format(trip.StartDate), StartKm: Km(trip.StartKm),
		PaidDate: dates.FormatOptional(trip.PaidDate), CreatedAt: trip.CreatedAt.UTC(),
	}
	if ret := trip.Return; ret != nil {
		end, distance := Km(ret.EndKm), Km(ret.EndKm.Sub(trip.StartKm))
		fuel := string(ret.FuelType)
		date := dates.Format(ret.Date)
		fuelRate, wearRate := ret.Billing.FuelRate.StringFixed(3), ret.Billing.WearRate.StringFixed(3)
		fuelAmount, wearAmount, total := money.Format(ret.Billing.FuelAmount), money.Format(ret.Billing.WearAmount), money.Format(ret.Billing.Total)
		out.ReturnDate, out.EndKm, out.DistanceKm, out.FuelType = &date, &end, &distance, &fuel
		out.FuelRate, out.WearRate = &fuelRate, &wearRate
		out.FuelAmount, out.WearAmount, out.TotalAmount = &fuelAmount, &wearAmount, &total
	}
	if photo, ok := trip.Photos[domain.LegStart]; ok {
		out.Photos.Start = p.photo(photo)
	}
	if photo, ok := trip.Photos[domain.LegEnd]; ok {
		out.Photos.End = p.photo(photo)
	}
	return out
}

// Trips renders a list; never null in JSON.
func (p Presenter) Trips(trips []domain.Trip) []TripResponse {
	out := make([]TripResponse, len(trips))
	for i, trip := range trips {
		out[i] = p.Trip(trip)
	}
	return out
}

func (p Presenter) photo(photo domain.Photo) *PhotoBody {
	expires := p.Clock.Now().Truncate(time.Hour).Add(photoURLLifetime + time.Hour)
	signed := p.Signer.Sign(photo.ID.String(), expires)
	query := url.Values{"exp": {fmt.Sprint(signed.Expires)}, "sig": {signed.Signature}}
	return &PhotoBody{
		URL:       fmt.Sprintf("%s/%s?%s", "/api/v1"+PhotosPath, photo.ID, query.Encode()),
		TakenAt:   photo.CreatedAt.UTC(),
		ExpiresAt: photo.CreatedAt.Add(p.Retention).UTC(),
	}
}

// Km renders an odometer reading as a JSON number without float noise.
func Km(km decimal.Decimal) json.Number {
	return json.Number(km.String())
}

// VoucherResponse is a voucher as the API sends it.
type VoucherResponse struct {
	ID          uuid.UUID  `json:"id"`
	Date        string     `json:"date"`
	Amount      string     `json:"amount"`
	SettledDate *string    `json:"settled_date"`
	Photo       *PhotoBody `json:"photo"`
	CreatedAt   time.Time  `json:"created_at"`
}

// voucherPhotoPath is where a voucher's photo is served, with the API prefix.
func voucherPhotoPath(id uuid.UUID) string { return "/api/v1/vouchers/" + id.String() + "/photo" }

// Voucher renders one voucher, signing its photo URL.
func (p Presenter) Voucher(voucher domain.Voucher) VoucherResponse {
	out := VoucherResponse{
		ID: voucher.ID, Date: dates.Format(voucher.Date), Amount: money.Format(voucher.Amount),
		SettledDate: dates.FormatOptional(voucher.SettledDate), CreatedAt: voucher.CreatedAt.UTC(),
	}
	if voucher.Photo != nil {
		expires := p.Clock.Now().Truncate(time.Hour).Add(photoURLLifetime + time.Hour)
		signed := p.Signer.Sign(voucher.ID.String(), expires)
		query := url.Values{"exp": {fmt.Sprint(signed.Expires)}, "sig": {signed.Signature}}
		out.Photo = &PhotoBody{
			URL:       voucherPhotoPath(voucher.ID) + "?" + query.Encode(),
			TakenAt:   voucher.Photo.CreatedAt.UTC(),
			ExpiresAt: voucher.Photo.CreatedAt.Add(p.Retention).UTC(),
		}
	}
	return out
}

// Vouchers renders a list; never null in JSON.
func (p Presenter) Vouchers(vouchers []domain.Voucher) []VoucherResponse {
	out := make([]VoucherResponse, len(vouchers))
	for i, voucher := range vouchers {
		out[i] = p.Voucher(voucher)
	}
	return out
}
