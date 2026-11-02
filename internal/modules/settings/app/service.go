// Package app holds the settings use cases.
package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/settings/domain"
)

// Repository stores settings.
type Repository interface {
	// Ensure creates the default row if the user has none and returns it.
	Ensure(ctx context.Context, userID uuid.UUID) (domain.Settings, error)
	UpdateRates(ctx context.Context, userID uuid.UUID, rates domain.Rates) (domain.Settings, error)
	UpdateProfile(ctx context.Context, userID uuid.UUID, carName, borrowerName string) (domain.Settings, error)
	RotateShareToken(ctx context.Context, userID uuid.UUID, token uuid.UUID) (domain.Settings, error)
	// ByShareToken returns domain.ErrNotFound if no settings carry the token.
	ByShareToken(ctx context.Context, token uuid.UUID) (domain.Settings, error)
}

// Service holds the settings use cases.
type Service struct {
	repo Repository
}

// NewService creates the service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Get returns the user's settings. They are created with defaults on first
// use, so a new account works without any setup step.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (domain.Settings, error) {
	return s.repo.Ensure(ctx, userID)
}

// RatesInput are the rates as the client sends them.
type RatesInput struct {
	Gasoline string
	LPG      string
	Wear     string
}

// UpdateRates changes the rates for the trips that come back from now on.
func (s *Service) UpdateRates(ctx context.Context, userID uuid.UUID, in RatesInput) (domain.Settings, error) {
	var problems domain.ValidationError
	rates := domain.Rates{
		Gasoline: domain.ParseRate("gasoline_rate", in.Gasoline, &problems),
		LPG:      domain.ParseRate("lpg_rate", in.LPG, &problems),
		Wear:     domain.ParseRate("wear_rate", in.Wear, &problems),
	}
	if err := problems.OrNil(); err != nil {
		return domain.Settings{}, err
	}
	if _, err := s.repo.Ensure(ctx, userID); err != nil {
		return domain.Settings{}, err
	}
	return s.repo.UpdateRates(ctx, userID, rates)
}

// UpdateProfile changes the car's and the borrower's names.
func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, carName, borrowerName string) (domain.Settings, error) {
	carName = domain.NormalizeName(carName)
	borrowerName = domain.NormalizeName(borrowerName)
	var problems domain.ValidationError
	domain.ValidateName("car_name", carName, domain.CarNameMaxLength, &problems)
	domain.ValidateName("borrower_name", borrowerName, domain.BorrowerNameMaxLength, &problems)
	if err := problems.OrNil(); err != nil {
		return domain.Settings{}, err
	}
	if _, err := s.repo.Ensure(ctx, userID); err != nil {
		return domain.Settings{}, err
	}
	return s.repo.UpdateProfile(ctx, userID, carName, borrowerName)
}

// RotateShareToken invalidates the current share link and makes a new one.
func (s *Service) RotateShareToken(ctx context.Context, userID uuid.UUID) (domain.Settings, error) {
	if _, err := s.repo.Ensure(ctx, userID); err != nil {
		return domain.Settings{}, err
	}
	return s.repo.RotateShareToken(ctx, userID, uuid.New())
}

// ByShareToken finds whose link it is.
func (s *Service) ByShareToken(ctx context.Context, token uuid.UUID) (domain.Settings, error) {
	return s.repo.ByShareToken(ctx, token)
}
