// Package app builds the page the borrower sees from the share link, with no
// login: what is owed and the trips' history. Never the fuel log.
package app

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings"
	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/trips"
)

// List sizes.
const (
	maxPending = 200
	maxHistory = 60
)

// ErrNotFound: the link is wrong, was rotated, or its owner was deactivated.
var ErrNotFound = errors.New("share link not found")

// Settings finds whose link it is.
type Settings interface {
	ByShareToken(ctx context.Context, token uuid.UUID) (settings.Settings, error)
}

// Owners gives an active owner's name; an error if the account is off.
type Owners interface {
	ActiveDisplayName(ctx context.Context, userID uuid.UUID) (string, error)
}

// Trips reads the owner's trips.
type Trips interface {
	ByStatus(ctx context.Context, userID uuid.UUID, statuses []trips.Status, limit int) ([]trips.Trip, error)
	OpenVouchers(ctx context.Context, userID uuid.UUID) ([]trips.Voucher, error)
}

// Page is what the borrower sees.
type Page struct {
	OwnerName    string
	Pending      []trips.Trip
	PendingTotal decimal.Decimal
	// Vouchers are the fuel the borrower already paid for: credit not yet discounted.
	Vouchers     []trips.Voucher
	VoucherTotal decimal.Decimal
	History      []trips.Trip
}

// Service builds the share page.
type Service struct {
	settings Settings
	owners   Owners
	trips    Trips
}

// NewService creates the service.
func NewService(s Settings, o Owners, t Trips) *Service {
	return &Service{settings: s, owners: o, trips: t}
}

// Get builds the page for a share token.
func (s *Service) Get(ctx context.Context, token uuid.UUID) (Page, error) {
	owner, err := s.settings.ByShareToken(ctx, token)
	if errors.Is(err, settings.ErrNotFound) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, err
	}
	name, err := s.owners.ActiveDisplayName(ctx, owner.UserID)
	if err != nil {
		return Page{}, ErrNotFound
	}
	page := Page{OwnerName: name}
	if page.Pending, err = s.trips.ByStatus(ctx, owner.UserID, []trips.Status{trips.StatusPending}, maxPending); err != nil {
		return Page{}, err
	}
	for _, trip := range page.Pending {
		page.PendingTotal = page.PendingTotal.Add(trip.Total())
	}
	if page.Vouchers, err = s.trips.OpenVouchers(ctx, owner.UserID); err != nil {
		return Page{}, err
	}
	for _, voucher := range page.Vouchers {
		page.VoucherTotal = page.VoucherTotal.Add(voucher.Amount)
	}
	billable := []trips.Status{trips.StatusPending, trips.StatusPaid}
	if page.History, err = s.trips.ByStatus(ctx, owner.UserID, billable, maxHistory); err != nil {
		return Page{}, err
	}
	return page, nil
}
