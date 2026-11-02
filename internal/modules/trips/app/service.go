package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// MaxPayAtOnce caps "Ya me pagó": nobody owes hundreds of trips.
const MaxPayAtOnce = 200

// Deps are the ports the service needs.
type Deps struct {
	Trips    Repository
	Photos   Photos
	Files    Files
	Rates    RatesProvider
	Tx       Transactor
	Clock    clock.Clock
	Location *time.Location
	// PhotoRetention is how long a photo is kept before the cleanup deletes it.
	PhotoRetention time.Duration
}

// Service holds the trips use cases.
type Service struct {
	deps Deps
}

// NewService creates the service.
func NewService(deps Deps) *Service { return &Service{deps: deps} }

func (s *Service) today() time.Time { return dates.Today(s.deps.Clock.Now(), s.deps.Location) }

// ReturnInput is the car coming back, as the client sends it.
type ReturnInput struct {
	Date     string
	EndKm    *decimal.Decimal
	FuelType string
}

// LendInput lends the car. With Return set, it registers a trip that is
// already over (the owner forgot to log it when the car left).
type LendInput struct {
	StartDate string
	StartKm   *decimal.Decimal
	Return    *ReturnInput
}

// Lend opens a trip. Only one can be in progress: the car cannot be lent twice.
func (s *Service) Lend(ctx context.Context, userID uuid.UUID, in LendInput) (domain.Trip, error) {
	today := s.today()
	var problems domain.ValidationError
	startDate := parseDate("start_date", in.StartDate, &problems)
	if !startDate.IsZero() {
		domain.ValidateDate("start_date", startDate, today, &problems)
	}
	startKm := requireKm("start_km", in.StartKm, &problems)
	trip := domain.Trip{
		ID: ids.New(), UserID: userID, Status: domain.StatusInProgress,
		StartDate: startDate, StartKm: startKm, CreatedAt: s.deps.Clock.Now(),
	}
	var ret parsedReturn
	if in.Return != nil {
		ret = parseReturn(*in.Return, &problems)
		if !startDate.IsZero() && ret.complete() {
			domain.ValidateReturn(startDate, startKm, ret.date, ret.endKm, today, &problems)
		}
	}
	if err := problems.OrNil(); err != nil {
		return domain.Trip{}, err
	}
	if in.Return != nil {
		rates, err := s.deps.Rates.Rates(ctx, userID)
		if err != nil {
			return domain.Trip{}, err
		}
		trip.Status = domain.StatusPending
		trip.Return = &domain.Return{
			Date: ret.date, EndKm: ret.endKm, FuelType: ret.fuel,
			Billing: domain.Bill(ret.endKm.Sub(startKm), ret.fuel, rates),
		}
	}
	if err := s.deps.Trips.Insert(ctx, trip); err != nil {
		return domain.Trip{}, err
	}
	return trip, nil
}

// Receive closes the trip in progress and bills it with the current rates.
func (s *Service) Receive(ctx context.Context, userID, tripID uuid.UUID, in ReturnInput) (domain.Trip, error) {
	var problems domain.ValidationError
	ret := parseReturn(in, &problems)
	if err := problems.OrNil(); err != nil {
		return domain.Trip{}, err
	}
	rates, err := s.deps.Rates.Rates(ctx, userID)
	if err != nil {
		return domain.Trip{}, err
	}
	err = s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		trip, err := s.deps.Trips.Lock(ctx, userID, tripID)
		if err != nil {
			return err
		}
		if trip.Status != domain.StatusInProgress {
			return domain.ErrWrongStatus
		}
		domain.ValidateReturn(trip.StartDate, trip.StartKm, ret.date, ret.endKm, s.today(), &problems)
		if err := problems.OrNil(); err != nil {
			return err
		}
		return s.deps.Trips.SaveReturn(ctx, userID, tripID, domain.Return{
			Date: ret.date, EndKm: ret.endKm, FuelType: ret.fuel,
			Billing: domain.Bill(ret.endKm.Sub(trip.StartKm), ret.fuel, rates),
		})
	})
	if err != nil {
		return domain.Trip{}, err
	}
	return s.Get(ctx, userID, tripID)
}

// Cancel drops a trip in progress: no amount, and the car is home again.
func (s *Service) Cancel(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
	return s.transition(ctx, userID, tripID, domain.StatusInProgress, domain.StatusCancelled, nil)
}

// MarkPending undoes a payment.
func (s *Service) MarkPending(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
	return s.transition(ctx, userID, tripID, domain.StatusPaid, domain.StatusPending, nil)
}

// MarkPaid marks one pending trip paid today.
func (s *Service) MarkPaid(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
	today := s.today()
	return s.transition(ctx, userID, tripID, domain.StatusPending, domain.StatusPaid, &today)
}

func (s *Service) transition(ctx context.Context, userID, tripID uuid.UUID, from, to domain.Status, paidDate *time.Time) (domain.Trip, error) {
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		trip, err := s.deps.Trips.Lock(ctx, userID, tripID)
		if err != nil {
			return err
		}
		if trip.Status != from {
			return domain.ErrWrongStatus
		}
		return s.deps.Trips.SetStatus(ctx, userID, tripID, to, paidDate)
	})
	if err != nil {
		return domain.Trip{}, err
	}
	return s.Get(ctx, userID, tripID)
}

// PayResult says how much was marked paid.
type PayResult struct {
	TripIDs []uuid.UUID
	Total   decimal.Decimal
}

// PayMany marks several pending trips paid ("Ya me pagó todo"). Trips that are
// no longer pending are skipped: paying twice is harmless.
func (s *Service) PayMany(ctx context.Context, userID uuid.UUID, tripIDs []uuid.UUID) (PayResult, error) {
	var problems domain.ValidationError
	switch {
	case len(tripIDs) == 0:
		problems.Add("trip_ids", domain.CodeRequired)
	case len(tripIDs) > MaxPayAtOnce:
		problems.Add("trip_ids", domain.CodeTooMany)
	}
	if err := problems.OrNil(); err != nil {
		return PayResult{}, err
	}
	paid, err := s.deps.Trips.Pay(ctx, userID, tripIDs, s.today())
	if err != nil {
		return PayResult{}, err
	}
	total := decimal.Zero
	for _, id := range paid {
		trip, err := s.deps.Trips.Get(ctx, userID, id)
		if err != nil {
			return PayResult{}, err
		}
		total = total.Add(trip.Total())
	}
	return PayResult{TripIDs: paid, Total: total}, nil
}

// Delete removes a cancelled trip and its photos from the history.
func (s *Service) Delete(ctx context.Context, userID, tripID uuid.UUID) error {
	var photos map[domain.Leg]domain.Photo
	err := s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		trip, err := s.deps.Trips.Lock(ctx, userID, tripID)
		if err != nil {
			return err
		}
		if trip.Status != domain.StatusCancelled {
			return domain.ErrWrongStatus
		}
		all, err := s.deps.Photos.OfTrips(ctx, []uuid.UUID{tripID})
		if err != nil {
			return err
		}
		photos = all[tripID]
		return s.deps.Trips.Delete(ctx, userID, tripID)
	})
	if err != nil {
		return err
	}
	for _, photo := range photos {
		s.deleteFile(ctx, photo.StorageKey)
	}
	return nil
}

// Get returns one trip with its photos.
func (s *Service) Get(ctx context.Context, userID, tripID uuid.UUID) (domain.Trip, error) {
	trip, err := s.deps.Trips.Get(ctx, userID, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	trips, err := s.withPhotos(ctx, []domain.Trip{trip})
	if err != nil {
		return domain.Trip{}, err
	}
	return trips[0], nil
}

// Open returns the trip in progress, or nil if the car is home.
func (s *Service) Open(ctx context.Context, userID uuid.UUID) (*domain.Trip, error) {
	trip, err := s.deps.Trips.Open(ctx, userID)
	if err != nil || trip == nil {
		return nil, err
	}
	trips, err := s.withPhotos(ctx, []domain.Trip{*trip})
	if err != nil {
		return nil, err
	}
	return &trips[0], nil
}

// Page is a page of trips.
type Page struct {
	Trips []domain.Trip
	Total int64
}

// List returns the history, newest first; status nil means every status.
func (s *Service) List(ctx context.Context, userID uuid.UUID, status *domain.Status, limit, offset int) (Page, error) {
	trips, total, err := s.deps.Trips.List(ctx, userID, status, limit, offset)
	if err != nil {
		return Page{}, err
	}
	trips, err = s.withPhotos(ctx, trips)
	return Page{Trips: trips, Total: total}, err
}

// ByStatus returns the newest trips with any of the statuses, with photos.
func (s *Service) ByStatus(ctx context.Context, userID uuid.UUID, statuses []domain.Status, limit int) ([]domain.Trip, error) {
	trips, err := s.deps.Trips.ByStatus(ctx, userID, statuses, limit)
	if err != nil {
		return nil, err
	}
	return s.withPhotos(ctx, trips)
}

// BillableSince adds up the billable trips that started since a date.
func (s *Service) BillableSince(ctx context.Context, userID uuid.UUID, since time.Time) (Totals, error) {
	return s.deps.Trips.BillableSince(ctx, userID, since)
}

// FuelChargedByMonth is the fuel charged per month since a date.
func (s *Service) FuelChargedByMonth(ctx context.Context, userID uuid.UUID, since time.Time) ([]MonthAmount, error) {
	return s.deps.Trips.FuelChargedByMonth(ctx, userID, since)
}

// MaxOdometer is the highest reading any trip registered.
func (s *Service) MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return s.deps.Trips.MaxOdometer(ctx, userID)
}

// LastFuelType is what the car came back with last time, to preselect it.
func (s *Service) LastFuelType(ctx context.Context, userID uuid.UUID) (*domain.FuelType, error) {
	return s.deps.Trips.LastFuelType(ctx, userID)
}

func (s *Service) withPhotos(ctx context.Context, trips []domain.Trip) ([]domain.Trip, error) {
	if len(trips) == 0 {
		return trips, nil
	}
	tripIDs := make([]uuid.UUID, len(trips))
	for i, trip := range trips {
		tripIDs[i] = trip.ID
	}
	photos, err := s.deps.Photos.OfTrips(ctx, tripIDs)
	if err != nil {
		return nil, err
	}
	for i := range trips {
		trips[i].Photos = photos[trips[i].ID]
	}
	return trips, nil
}

func (s *Service) deleteFile(ctx context.Context, key string) {
	if err := s.deps.Files.Delete(key); err != nil {
		// The cleanup's sweep removes it later.
		logger.From(ctx).Warn("photo file not deleted", "error", err)
	}
}

func parseDate(field, text string, problems *domain.ValidationError) time.Time {
	if text == "" {
		problems.Add(field, domain.CodeRequired)
		return time.Time{}
	}
	date, err := dates.Parse(text)
	if err != nil {
		problems.Add(field, domain.CodeInvalid)
		return time.Time{}
	}
	return date
}

type parsedReturn struct {
	date  time.Time
	endKm decimal.Decimal
	fuel  domain.FuelType
	hasKm bool
}

func (r parsedReturn) complete() bool { return !r.date.IsZero() && r.hasKm }

func parseReturn(in ReturnInput, problems *domain.ValidationError) parsedReturn {
	ret := parsedReturn{date: parseDate("return_date", in.Date, problems)}
	if in.EndKm == nil {
		problems.Add("end_km", domain.CodeRequired)
	} else {
		ret.endKm, ret.hasKm = *in.EndKm, true
	}
	fuel, ok := domain.ParseFuelType(in.FuelType)
	if !ok {
		problems.Add("fuel_type", domain.CodeInvalid)
	}
	ret.fuel = fuel
	return ret
}

func requireKm(field string, km *decimal.Decimal, problems *domain.ValidationError) decimal.Decimal {
	if km == nil {
		problems.Add(field, domain.CodeRequired)
		return decimal.Zero
	}
	domain.ValidateKm(field, *km, problems)
	return *km
}
