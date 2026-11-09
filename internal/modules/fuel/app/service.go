// Package app holds the fuel log use cases.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/fuel/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/clock"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/dates"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
)

// MaxSummaryMonths caps how far back one summary goes.
const MaxSummaryMonths = 36

// Repository stores fuel logs. Every method is scoped to the user.
type Repository interface {
	Insert(ctx context.Context, log domain.Log) error
	// Delete returns domain.ErrNotFound if the user has no such log.
	Delete(ctx context.Context, userID, id uuid.UUID) error
	List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Log, int64, error)
	Since(ctx context.Context, userID uuid.UUID, since time.Time) ([]domain.Log, error)
	HasBefore(ctx context.Context, userID uuid.UUID, before time.Time) (bool, error)
	SpentSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error)
	MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error)
	CostPerKm(ctx context.Context, userID uuid.UUID) (*decimal.Decimal, error)
}

// MonthAmount is an amount for a calendar month (its first day).
type MonthAmount struct {
	Month  time.Time
	Amount decimal.Decimal
}

// ChargedProvider gives the fuel charged on trips per month (the trips module).
type ChargedProvider func(ctx context.Context, userID uuid.UUID, since time.Time) ([]MonthAmount, error)

// Deps are what the service needs.
type Deps struct {
	Logs     Repository
	Charged  ChargedProvider
	Clock    clock.Clock
	Location *time.Location
}

// Service holds the fuel log use cases.
type Service struct {
	deps Deps
}

// NewService creates the service.
func NewService(deps Deps) *Service { return &Service{deps: deps} }

func (s *Service) today() time.Time { return dates.Today(s.deps.Clock.Now(), s.deps.Location) }

// AddInput is a fill-up as the client sends it.
type AddInput struct {
	Date     string
	Odometer *decimal.Decimal
	FuelType string
	Amount   *decimal.Decimal
	IsFull   bool
	// PricePerGallon is optional.
	PricePerGallon *decimal.Decimal
}

// Add registers a fill-up.
func (s *Service) Add(ctx context.Context, userID uuid.UUID, in AddInput) (domain.Log, error) {
	var problems domain.ValidationError
	log := domain.Log{ID: ids.New(), UserID: userID, IsFull: in.IsFull, CreatedAt: s.deps.Clock.Now()}
	switch date, err := dates.Parse(in.Date); {
	case in.Date == "":
		problems.Add("date", domain.CodeRequired)
	case err != nil:
		problems.Add("date", domain.CodeInvalid)
	case date.After(s.today()):
		problems.Add("date", domain.CodeInFuture)
	default:
		log.Date = date
	}
	if in.Odometer == nil {
		problems.Add("odometer", domain.CodeRequired)
	} else {
		log.Odometer = *in.Odometer
		domain.ValidateOdometer(log.Odometer, &problems)
	}
	fuel, ok := domain.ParseFuelType(in.FuelType)
	if !ok {
		problems.Add("fuel_type", domain.CodeInvalid)
	}
	log.FuelType = fuel
	if in.Amount == nil {
		problems.Add("amount", domain.CodeRequired)
	} else {
		log.Amount = *in.Amount
		domain.ValidateAmount(log.Amount, &problems)
	}
	if in.PricePerGallon != nil {
		log.PricePerGallon = in.PricePerGallon
		domain.ValidatePricePerGallon(*in.PricePerGallon, &problems)
	}
	if err := problems.OrNil(); err != nil {
		return domain.Log{}, err
	}
	if err := s.deps.Logs.Insert(ctx, log); err != nil {
		return domain.Log{}, err
	}
	return log, nil
}

// Delete removes a fill-up.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	return s.deps.Logs.Delete(ctx, userID, id)
}

// Page is a page of fill-ups.
type Page struct {
	Logs  []domain.Log
	Total int64
}

// List returns fill-ups, newest first.
func (s *Service) List(ctx context.Context, userID uuid.UUID, limit, offset int) (Page, error) {
	logs, total, err := s.deps.Logs.List(ctx, userID, limit, offset)
	return Page{Logs: logs, Total: total}, err
}

// Month is one month of the summary.
type Month struct {
	Month   time.Time
	Spent   decimal.Decimal
	Charged decimal.Decimal
	Logs    []domain.Log
}

// Summary compares what was spent on fuel with what the trips charged for it,
// month by month, newest first.
type Summary struct {
	Months    []Month
	CostPerKm *decimal.Decimal
	// HasMore: there are fill-ups before the first month shown.
	HasMore bool
}

// Summary covers the last months (this one included).
func (s *Service) Summary(ctx context.Context, userID uuid.UUID, months int) (Summary, error) {
	months = min(max(months, 1), MaxSummaryMonths)
	since := dates.MonthStart(s.today()).AddDate(0, -(months - 1), 0)

	logs, err := s.deps.Logs.Since(ctx, userID, since)
	if err != nil {
		return Summary{}, err
	}
	charged, err := s.deps.Charged(ctx, userID, since)
	if err != nil {
		return Summary{}, err
	}
	hasMore, err := s.deps.Logs.HasBefore(ctx, userID, since)
	if err != nil {
		return Summary{}, err
	}
	cost, err := s.deps.Logs.CostPerKm(ctx, userID)
	if err != nil {
		return Summary{}, err
	}

	byMonth := map[time.Time]*Month{}
	get := func(month time.Time) *Month {
		if byMonth[month] == nil {
			byMonth[month] = &Month{Month: month, Logs: []domain.Log{}}
		}
		return byMonth[month]
	}
	for _, log := range logs {
		month := get(dates.MonthStart(log.Date))
		month.Logs = append(month.Logs, log)
		month.Spent = month.Spent.Add(log.Amount)
	}
	for _, amount := range charged {
		get(dates.MonthStart(amount.Month)).Charged = amount.Amount
	}
	summary := Summary{Months: []Month{}, CostPerKm: cost, HasMore: hasMore}
	for month := dates.MonthStart(s.today()); !month.Before(since); month = month.AddDate(0, -1, 0) {
		if m, ok := byMonth[month]; ok {
			summary.Months = append(summary.Months, *m)
		}
	}
	return summary, nil
}

// SpentSince is what was spent on fuel since a date.
func (s *Service) SpentSince(ctx context.Context, userID uuid.UUID, since time.Time) (decimal.Decimal, error) {
	return s.deps.Logs.SpentSince(ctx, userID, since)
}

// MaxOdometer is the highest reading any fill-up registered.
func (s *Service) MaxOdometer(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	return s.deps.Logs.MaxOdometer(ctx, userID)
}
