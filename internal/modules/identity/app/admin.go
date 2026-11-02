package app

import (
	"context"
	"fmt"

	"github.com/AtmosferaSoltec/atm-km-api/internal/modules/identity/domain"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/ids"
	"github.com/AtmosferaSoltec/atm-km-api/internal/platform/logger"
)

// There is no sign-up or password recovery endpoint: the owner manages accounts
// from the server with the `api user ...` subcommands, which call these.

// CreateUserInput is a new account.
type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
}

// CreateUser validates and stores a new account with its password hashed.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (domain.User, error) {
	username := domain.NormalizeUsername(in.Username)
	displayName := domain.NormalizeDisplayName(in.DisplayName)

	var problems domain.ValidationError
	domain.ValidateUsername(username, &problems)
	domain.ValidateDisplayName(displayName, &problems)
	domain.ValidateNewPassword(in.Password, &problems)
	if err := problems.OrNil(); err != nil {
		return domain.User{}, err
	}

	hash, err := s.deps.Hasher.Hash(in.Password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	user := domain.User{
		ID: ids.New(), Username: username, DisplayName: displayName,
		PasswordHash: hash, IsActive: true, CreatedAt: s.deps.Clock.Now(),
	}
	if err := s.deps.Users.Create(ctx, user); err != nil {
		return domain.User{}, err
	}
	logger.From(ctx).Info("security: user created", "user_id", user.ID)
	return user, nil
}

// ChangePassword sets a new password and closes every open session.
func (s *Service) ChangePassword(ctx context.Context, username, password string) error {
	var problems domain.ValidationError
	domain.ValidateNewPassword(password, &problems)
	if err := problems.OrNil(); err != nil {
		return err
	}
	user, err := s.deps.Users.ByUsername(ctx, domain.NormalizeUsername(username))
	if err != nil {
		return err
	}
	hash, err := s.deps.Hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.deps.Users.SetPassword(ctx, user.ID, hash); err != nil {
			return err
		}
		return s.deps.Sessions.RevokeAll(ctx, user.ID, revokedPassword)
	})
}

// SetActive turns an account on or off. Turning it off closes every session at
// once and hides its share link; its history stays.
func (s *Service) SetActive(ctx context.Context, username string, active bool) error {
	user, err := s.deps.Users.ByUsername(ctx, domain.NormalizeUsername(username))
	if err != nil {
		return err
	}
	return s.deps.Tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.deps.Users.SetActive(ctx, user.ID, active); err != nil {
			return err
		}
		if active {
			return nil
		}
		return s.deps.Sessions.RevokeAll(ctx, user.ID, revokedDeactivated)
	})
}
